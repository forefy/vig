package chain

import "testing"

const pid = "GauwsjaNa7fmSRwCg9APFhZMkgxSz31q5HfNuLtRR522"

func TestParseLogsBuildsTheCallTree(t *testing.T) {
	logs := []string{
		"Program ComputeBudget111111111111111111111111111111 invoke [1]",
		"Program ComputeBudget111111111111111111111111111111 success",
		"Program " + pid + " invoke [1]",
		"Program log: Instruction: LiquidUnstake",
		"Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA invoke [2]",
		"Program log: Instruction: Transfer",
		"Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA consumed 4645 of 180000 compute units",
		"Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA success",
		"Program 11111111111111111111111111111111 invoke [2]",
		"Program 11111111111111111111111111111111 success",
		"Program " + pid + " consumed 41212 of 200000 compute units",
		"Program " + pid + " success",
	}
	tr := ParseLogs(logs)
	if len(tr.Top) != 2 || len(tr.All) != 4 {
		t.Fatalf("frames: top=%d all=%d", len(tr.Top), len(tr.All))
	}
	f := tr.Top[1]
	if f.Program != pid || f.Depth != 1 || f.Seq != 2 || f.Instruction != "liquid_unstake" || f.Units != 41212 || f.Failed {
		t.Errorf("frame: %+v", f)
	}
	if got := f.Callees(); len(got) != 2 || got[0] != "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA" || got[1] != "11111111111111111111111111111111" {
		t.Errorf("callees: %v", got)
	}
	if f.Children[0].Parent != f || f.Children[0].Depth != 2 || f.Children[0].Instruction != "transfer" || f.Children[0].Units != 4645 {
		t.Errorf("child: %+v", f.Children[0])
	}
	if f.Children[0].Root() != f {
		t.Error("root")
	}
}

func TestParseLogsFailureUnwindsAndNamesTheCode(t *testing.T) {
	logs := []string{
		"Program Aggreg8r111111111111111111111111111111111 invoke [1]",
		"Program " + pid + " invoke [2]",
		"Program log: Instruction: Deposit",
		"Program log: AnchorError occurred. Error Code: InsufficientFunds. Error Number: 6001. Error Message: not enough.",
		"Program " + pid + " consumed 9000 of 100000 compute units",
		"Program " + pid + " failed: custom program error: 0x1771",
		"Program Aggreg8r111111111111111111111111111111111 consumed 12000 of 200000 compute units",
		"Program Aggreg8r111111111111111111111111111111111 failed: custom program error: 0x1771",
	}
	tr := ParseLogs(logs)
	if len(tr.All) != 2 {
		t.Fatalf("frames %d", len(tr.All))
	}
	outer, inner := tr.All[0], tr.All[1]
	if !inner.Failed || inner.Error != "InsufficientFunds" || inner.ErrorCode != 6001 || inner.Instruction != "deposit" {
		t.Errorf("inner: %+v", inner)
	}
	if !outer.Failed || outer.ErrorCode != 6001 || inner.Parent != outer {
		t.Errorf("outer: %+v", outer)
	}
}

func TestParseLogsRawFailureAndTruncation(t *testing.T) {
	logs := []string{
		"Program " + pid + " invoke [1]",
		"Program " + pid + " failed: insufficient funds for instruction",
		"Program " + pid + " invoke [1]",
		"Log truncated",
	}
	tr := ParseLogs(logs)
	if len(tr.All) != 2 || !tr.All[0].Failed || tr.All[0].Error != "insufficient funds for instruction" || tr.All[0].ErrorCode != 0 {
		t.Errorf("raw: %+v", tr.All[0])
	}
	if !tr.Truncated || !tr.All[1].Truncated || tr.All[0].Truncated {
		t.Errorf("truncated: %v %v %v", tr.Truncated, tr.All[1].Truncated, tr.All[0].Truncated)
	}
}

func TestSnakeName(t *testing.T) {
	for in, want := range map[string]string{"Deposit": "deposit", "LiquidUnstake": "liquid_unstake", "SetAdminV2": "set_admin_v2", "CPIDeposit": "cpi_deposit", "already_snake": "already_snake", "": ""} {
		if got := snakeName(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestCustomCode(t *testing.T) {
	if n, ok := customCode("custom program error: 0x1771"); !ok || n != 6001 {
		t.Errorf("hex: %d %v", n, ok)
	}
	if n, ok := customCode("custom program error: 42"); !ok || n != 42 {
		t.Errorf("dec: %d %v", n, ok)
	}
	if _, ok := customCode("insufficient funds"); ok {
		t.Error("no code")
	}
}
