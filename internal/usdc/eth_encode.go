package usdc

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// transferSelector is the first 4 bytes of keccak256("transfer(address,uint256)").
// Precomputed at init to avoid recomputing on every encode call.
var transferSelector = func() [4]byte {
	h := crypto.Keccak256([]byte("transfer(address,uint256)"))
	var sel [4]byte
	copy(sel[:], h[:4])
	return sel
}()

// EncodeTransferCall returns the 68-byte ABI-encoded calldata for
// USDC.transfer(to, amount). Equivalent to go-ethereum/accounts/abi
// but without the reflection overhead — the shape is fixed.
//
// Layout:
//
//	[0:4]   selector = keccak256("transfer(address,uint256)")[:4]
//	[4:36]  address (32-byte, left-padded with zeros)
//	[36:68] amount  (32-byte, big-endian uint256)
func EncodeTransferCall(to string, amount *big.Int) ([]byte, error) {
	if !isHexAddress(to) {
		return nil, fmt.Errorf("encode transfer: %w", ErrBadRecipient)
	}
	if amount == nil || amount.Sign() <= 0 {
		return nil, ErrAmountNonPositive
	}
	if amount.BitLen() > 256 {
		return nil, errors.New("usdc: amount exceeds uint256")
	}

	toAddr := common.HexToAddress(to)

	out := make([]byte, 68)
	copy(out[0:4], transferSelector[:])

	// Address goes in bytes [16:36] so it's right-aligned in the 32-byte slot.
	copy(out[4+12:36], toAddr.Bytes())

	// uint256 big-endian, right-aligned in 32 bytes.
	amtBytes := amount.Bytes()
	copy(out[68-len(amtBytes):68], amtBytes)

	return out, nil
}

// DecodeTransferCall reverses EncodeTransferCall. Useful for tests and
// for ops tooling that needs to render a raw tx payload.
func DecodeTransferCall(data []byte) (to string, amount *big.Int, err error) {
	if len(data) != 68 {
		return "", nil, fmt.Errorf("usdc: expected 68-byte transfer calldata, got %d", len(data))
	}
	if !bytes.Equal(data[0:4], transferSelector[:]) {
		return "", nil, errors.New("usdc: wrong function selector")
	}
	addr := common.BytesToAddress(data[4:36])
	amt := new(big.Int).SetBytes(data[36:68])
	return strings.ToLower(addr.Hex()), amt, nil
}

// TransferSelectorHex returns the 4-byte selector as a 0x-prefixed string.
// Used in tests and log lines.
func TransferSelectorHex() string {
	return "0x" + common.Bytes2Hex(transferSelector[:])
}
