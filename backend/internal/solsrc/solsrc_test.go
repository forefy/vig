package solsrc

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forefy/vig/backend/internal/evm"
)

func write(t *testing.T, root, rel, src string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadResolvesStructsEnumsAndOverloads(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/interfaces/IBoard.sol", `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;
interface IBoard {
    enum Side { Buy, Sell }
    struct Order {
        address maker; // who
        uint128 amount;
        Side side;
        IERC20 token;
    }
    event Filled(uint256 indexed id, address indexed taker, uint128 amount);
    error Nope(uint256 id);
    function fill(uint256 id, uint128 amount) external payable returns (bool);
    function fill(uint256 id) external;
}
`)
	write(t, root, "src/Board.sol", `pragma solidity ^0.8.20;
import "./interfaces/IBoard.sol";
contract Board is IBoard, Owned {
    mapping(uint256 => Order) internal orders;
    /* a comment with function fake(uint) inside */
    function create(Order calldata order, bytes[] memory sigs) external nonReentrant returns (uint256) {
        IERC20(order.token).transferFrom(msg.sender, address(this), order.amount);
        (bool ok, ) = msg.sender.call{value: 1}("");
        unchecked { x += 1; }
        assembly { let y := 0 }
        return 1;
    }
    function fill(uint256 id, uint128 amount) external payable override returns (bool) { return true; }
    function fill(uint256 id) external override {}
    function _internal(uint a) internal pure returns (uint) { return a; }
    receive() external payable {}
}
`)
	write(t, root, "test/Board.t.sol", `contract BoardTest { function testFill() public {} }`)
	m, err := Load(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Files != 2 {
		t.Fatalf("files %d (tests must be skipped)", m.Files)
	}
	c, ok := m.ByName("Board")
	if !ok || c.Kind != "contract" || c.File != "src/Board.sol" || len(c.Inherits) != 2 {
		t.Fatalf("contract: %+v %v", c, ok)
	}
	sel := func(sig string) string { h := evm.Keccak([]byte(sig)); return hex.EncodeToString(h[:4]) }
	want := map[string]string{
		"create": sel("create((address,uint128,uint8,address),bytes[])"),
		"fill":   sel("fill(uint256,uint128)"),
	}
	got := map[string]string{}
	for _, f := range c.Functions {
		if f.Name == "create" || (f.Name == "fill" && len(f.Params) == 2) {
			got[f.Name] = hex.EncodeToString(f.Selector[:])
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s selector %s want %s", k, got[k], v)
		}
	}
	exposed := m.Exposed(c)
	if len(exposed) != 3 {
		t.Errorf("exposed %d: %+v", len(exposed), exposed)
	}
	var create Function
	for _, f := range c.Functions {
		if f.Name == "create" {
			create = f
		}
	}
	if create.Visibility != "external" || len(create.Modifiers) != 1 || create.Modifiers[0] != "nonReentrant" || create.Params[0].Type != "tuple" || create.Params[0].Fields[2].Type != "uint8" || create.Params[1].Type != "bytes[]" {
		t.Errorf("create: %+v", create)
	}
	if c.Sites.Calls != 2 || c.Sites.Transfers != 1 || c.Sites.Unchecked != 1 || c.Sites.Assembly != 1 || c.Sites.Payable != 2 {
		t.Errorf("sites: %+v", c.Sites)
	}
	ib, _ := m.ByName("IBoard")
	if len(ib.Events) != 1 || ib.Events[0].Sig != "Filled(uint256,address,uint128)" || !ib.Events[0].Params[0].Indexed || len(ib.Errors) != 1 {
		t.Errorf("interface: %+v", ib.Events)
	}
	abi := c.Interface()
	if len(abi.Functions) != 3 {
		t.Errorf("interface functions %d", len(abi.Functions))
	}
}

// A checkout of an orderbook DEX against the ABI Sourcify verified for its
// deployment: every function selector on chain must come out of the
// source scan. ORDERBOOK_DIR is the checkout, ORDERBOOK_ABI the Sourcify
// answer saved to a file.
func TestOrderbookSourceMatchesVerifiedABI(t *testing.T) {
	dir, abiPath := os.Getenv("ORDERBOOK_DIR"), os.Getenv("ORDERBOOK_ABI")
	if dir == "" || abiPath == "" {
		t.Skip("ORDERBOOK_DIR and ORDERBOOK_ABI not set")
	}
	raw, err := os.ReadFile(abiPath)
	if err != nil {
		t.Skip(err)
	}
	var body struct {
		ABI json.RawMessage `json:"abi"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	abi, err := evm.ParseABI(body.ABI)
	if err != nil {
		t.Fatal(err)
	}
	model, err := Load(dir, "contracts")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := model.ByName("Orderbook")
	if !ok {
		t.Fatal("no Orderbook contract")
	}
	have := map[[4]byte]string{}
	for _, f := range model.Exposed(c) {
		have[f.Selector] = f.Sig
	}
	missing := 0
	for sel, f := range abi.Functions {
		if _, ok := have[sel]; !ok {
			missing++
			t.Logf("missing %s", f.Sig)
		}
	}
	events := map[[32]byte]bool{}
	for _, e := range c.Events {
		events[e.Topic] = true
	}
	for _, in := range c.Inherits {
		if p, ok := model.ByName(in); ok {
			for _, e := range p.Events {
				events[e.Topic] = true
			}
		}
	}
	missingEvents := 0
	for topic, e := range abi.Events {
		if !events[topic] {
			missingEvents++
			t.Logf("missing event %s", e.Sig)
		}
	}
	t.Logf("abi functions %d, source exposed %d, missing %d; abi events %d, missing %d", len(abi.Functions), len(have), missing, len(abi.Events), missingEvents)
	if missing > 0 || missingEvents > 0 {
		t.Errorf("%d functions and %d events on chain not derived from source", missing, missingEvents)
	}
}

func TestValueTypesResolveToTheirUnderlyingType(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/Types.sol", `pragma solidity ^0.8.20;
type Token is address;
type Amount is uint;
`)
	write(t, root, "src/Vault.sol", `pragma solidity ^0.8.20;
import "./Types.sol";
contract Vault {
    type Shares is uint128;
    struct Pos { Token token; Amount amount; }
    function deposit(Token token, Amount amount, Shares[] calldata s, Pos calldata p) external {}
}
`)
	m, err := Load(root, "")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := m.ByName("Vault")
	if !ok || len(c.Functions) != 1 {
		t.Fatalf("vault: %+v", c)
	}
	if got, want := c.Functions[0].Sig, "deposit(address,uint256,uint128[],(address,uint256))"; got != want {
		t.Errorf("sig %s want %s", got, want)
	}
}

func TestReachFollowsHelpersToOutsideContracts(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/Board.sol", `pragma solidity ^0.8.20;
interface ISignatureTransfer { function permitTransferFrom(uint256 a) external; }
contract Board {
    ISignatureTransfer private constant _PERMIT2 = ISignatureTransfer(0x000000000022D473030F116dDEE9F6B43aC78BA3);
    function fill(uint256 id) external { _pay(id); _settle(); }
    function fillPermit(uint256 id, uint256 a) external { _PERMIT2.permitTransferFrom(a); _pay(id); }
    function cancel(uint256 id) external { _refund(id); }
    function _pay(uint256 id) private { IERC20(address(uint160(id))).transferFrom(msg.sender, address(this), 1); }
    function _settle() private { _refund(1); }
    function _refund(uint256 id) private { payable(msg.sender).sendValue(id); }
    function noop() external pure returns (uint256) { return 1; }
}
`)
	m, err := Load(root, "")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := m.ByName("Board")
	names := func(fn string) string {
		for _, f := range c.Functions {
			if f.Name == fn {
				var out []string
				for _, r := range m.Reach(c, f) {
					out = append(out, r.Name+"@"+r.Address)
				}
				return strings.Join(out, " ")
			}
		}
		return "?"
	}
	if got := names("fill"); got != "ERC-20 token@ ETH recipient@" {
		t.Errorf("fill reaches %q", got)
	}
	if got := names("fillPermit"); got != "ISignatureTransfer@0x000000000022d473030f116ddee9f6b43ac78ba3 ERC-20 token@" {
		t.Errorf("fillPermit reaches %q", got)
	}
	if got := names("cancel"); got != "ETH recipient@" {
		t.Errorf("cancel reaches %q", got)
	}
	if got := names("noop"); got != "" {
		t.Errorf("noop reaches %q", got)
	}
}
