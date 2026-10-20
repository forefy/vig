package chain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"time"
)

// ProgramFacts is everything the verification panel needs from chain.
type ProgramFacts struct {
	ProgramID        string
	Exists           bool
	Executable       bool
	Loader           string // upgradeable, non-upgradeable, unknown
	LoaderProgram    string
	UpgradeAuthority string // empty when immutable or not upgradeable
	LastDeploySlot   uint64
	DeployedAt       time.Time
	BytecodeSHA256   string
	Bytecode         []byte // the deployed image, for offline profiling
	IDLPresent       bool
	IDLVersion       string
	IDLRepository    string // metadata.repository when the IDL names one
	Slot             uint64
}

func decodeBase64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// HashBytecode hashes program data with trailing zero padding removed, the
// same normalisation solana-verify applies before comparing.
func HashBytecode(b []byte) string { return hashBytecode(b) }

func hashBytecode(b []byte) string {
	b = bytes.TrimRight(b, "\x00")
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Program fetches the facts for a program id.
func (c *Client) Program(ctx context.Context, programID string) (ProgramFacts, error) {
	f := ProgramFacts{ProgramID: programID, Loader: "unknown"}
	acct, err := c.GetAccount(ctx, programID)
	if err != nil {
		return f, err
	}
	f.Slot = acct.Slot
	if !acct.Exists {
		return f, nil
	}
	f.Exists = true
	f.Executable = acct.Executable
	f.LoaderProgram = acct.Owner
	switch acct.Owner {
	case BPFLoaderUpgradeable:
		f.Loader = "upgradeable"
		if len(acct.Data) >= 36 && binary.LittleEndian.Uint32(acct.Data[:4]) == 2 {
			pdAddr := Encode(acct.Data[4:36])
			// Read the 45-byte header (kind, slot, authority) through the
			// short-lived cache, then the bytes at that slot through the
			// immutable cache: a redeploy changes the slot and so the key.
			hdr, err := c.GetAccountSlice(ctx, pdAddr, 0, 45)
			if err != nil {
				return f, err
			}
			if hdr.Exists && len(hdr.Data) >= 45 && binary.LittleEndian.Uint32(hdr.Data[:4]) == 3 {
				f.LastDeploySlot = binary.LittleEndian.Uint64(hdr.Data[4:12])
				if hdr.Data[12] == 1 {
					f.UpgradeAuthority = Encode(hdr.Data[13:45])
				}
				pd, err := c.GetAccountAtSlot(ctx, pdAddr, f.LastDeploySlot)
				if err != nil {
					return f, err
				}
				if pd.Exists && len(pd.Data) >= 45 {
					f.Bytecode = pd.Data[45:]
					f.BytecodeSHA256 = hashBytecode(f.Bytecode)
				}
			}
		}
	case BPFLoader2, BPFLoader1:
		f.Loader = "non-upgradeable"
		f.BytecodeSHA256 = hashBytecode(acct.Data)
	case LoaderV4:
		f.Loader = "loader_v4"
		if len(acct.Data) >= 48 {
			f.LastDeploySlot = binary.LittleEndian.Uint64(acct.Data[:8])
			f.UpgradeAuthority = Encode(acct.Data[8:40])
			f.Bytecode = acct.Data[48:]
			f.BytecodeSHA256 = hashBytecode(f.Bytecode)
		} else {
			f.Bytecode = acct.Data
			f.BytecodeSHA256 = hashBytecode(acct.Data)
		}
	default:
		if acct.Executable {
			f.Loader = "non-upgradeable"
		}
		f.BytecodeSHA256 = hashBytecode(acct.Data)
	}
	if f.LastDeploySlot > 0 {
		if t, err := c.GetBlockTime(ctx, f.LastDeploySlot); err == nil {
			f.DeployedAt = t
		}
	}
	if idlAddr, err := AnchorIDLAddress(programID); err == nil {
		if idl, err := c.GetAccount(ctx, idlAddr); err == nil && idl.Exists && idl.Owner == programID {
			f.IDLPresent = true
			f.IDLVersion, f.IDLRepository = idlMeta(idl.Data)
		}
	}
	return f, nil
}

// idlMeta returns the version and repository an Anchor IDL account declares.
func idlMeta(data []byte) (version, repository string) {
	raw, err := decodeIDL(data)
	if err != nil {
		return "", ""
	}
	var idl struct {
		Version    string `json:"version"`
		Repository string `json:"repository"`
		Metadata   struct {
			Version    string `json:"version"`
			Spec       string `json:"spec"`
			Repository string `json:"repository"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &idl) != nil {
		return "", ""
	}
	version, repository = idl.Version, idl.Repository
	if idl.Metadata.Version != "" {
		version = idl.Metadata.Version
	}
	if idl.Metadata.Repository != "" {
		repository = idl.Metadata.Repository
	}
	return version, repository
}
