package chain

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"io"
)

// ErrNoIDL is returned when a program has no readable Anchor IDL on chain.
var ErrNoIDL = errors.New("no idl")

// IDL returns a program's decompressed Anchor IDL JSON from its IDL account.
func (c *Client) IDL(ctx context.Context, programID string) ([]byte, error) {
	addr, err := AnchorIDLAddress(programID)
	if err != nil {
		return nil, err
	}
	acct, err := c.GetAccount(ctx, addr)
	if err != nil {
		return nil, err
	}
	if !acct.Exists || acct.Owner != programID {
		return nil, ErrNoIDL
	}
	return decodeIDL(acct.Data)
}

// decodeIDL unwraps the IDL account layout: 8 byte discriminator, 32 byte
// authority, u32 length, zlib JSON.
func decodeIDL(data []byte) ([]byte, error) {
	if len(data) < 44 {
		return nil, ErrNoIDL
	}
	n := int(binary.LittleEndian.Uint32(data[40:44]))
	if n <= 0 || 44+n > len(data) {
		return nil, ErrNoIDL
	}
	zr, err := zlib.NewReader(bytes.NewReader(data[44 : 44+n]))
	if err != nil {
		return nil, ErrNoIDL
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, 8<<20))
	if err != nil || len(raw) == 0 {
		return nil, ErrNoIDL
	}
	return raw, nil
}
