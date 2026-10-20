package chain

import (
	"context"
	"encoding/binary"
	"errors"
)

// ProgramDataAddress returns the program-data account an upgradeable
// program points at, read from the cached program account. Its signature
// list is the program's deploy history. Other loaders have no such
// account and answer an error.
func (c *Client) ProgramDataAddress(ctx context.Context, programID string) (string, error) {
	acct, err := c.GetAccount(ctx, programID)
	if err != nil {
		return "", err
	}
	if !acct.Exists || acct.Owner != BPFLoaderUpgradeable {
		return "", errors.New("not an upgradeable program")
	}
	return programDataAddress(acct.Data)
}

// programDataAddress reads the Program variant of the upgradeable loader
// state: tag 2, then the 32-byte program-data pubkey.
func programDataAddress(data []byte) (string, error) {
	if len(data) < 36 || binary.LittleEndian.Uint32(data[:4]) != 2 {
		return "", errors.New("program account is not a loader Program state")
	}
	return Encode(data[4:36]), nil
}
