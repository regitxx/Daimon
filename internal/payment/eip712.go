package payment

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
)

// EIP-712 typed-data hashing for EIP-3009 `transferWithAuthorization`.
//
// EIP-712 reference: https://eips.ethereum.org/EIPS/eip-712
// EIP-3009 reference: https://eips.ethereum.org/EIPS/eip-3009
//
// Final digest is:
//
//	keccak256(0x1901 || domainSeparator || structHash)
//
// where:
//
//	domainSeparator = keccak256(abi.encode(
//	    keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"),
//	    keccak256(name), keccak256(version), chainId, verifyingContract))
//
//	structHash      = keccak256(abi.encode(
//	    keccak256("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"),
//	    from, to, value, validAfter, validBefore, nonce))
//
// All scalars are 32-byte right-padded big-endian per Solidity ABI; addresses
// are zero-padded on the left to 32 bytes; bytes32 is taken as-is.

// EIP-712 type-string hashes — keccak256 of the canonical type-encoding
// strings, precomputed at package-init time and cached.
var (
	eip712DomainTypeHash             = keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	transferWithAuthorizationTypeHash = keccak256([]byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"))
)

// keccak256 hashes b with the legacy Keccak-256 (the variant Ethereum
// adopted in 2015, *before* the NIST FIPS-202 SHA3 standardisation). All
// EVM signature schemes use this variant; the standard SHA3 family is a
// different hash and is NOT interchangeable.
func keccak256(b []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(b)
	return h.Sum(nil)
}

// padLeft32 left-pads s to 32 bytes. Used for the ABI encoding of every
// 256-bit scalar (addresses padded from 20→32, uint256 from <=32→32).
func padLeft32(s []byte) []byte {
	if len(s) >= 32 {
		return s[len(s)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(s):], s)
	return out
}

// hexToBytes is a forgiving 0x-prefix-optional hex decoder for the wire
// strings we receive (addresses, nonces, signatures). Wraps the std
// hex.DecodeString to strip a leading "0x" when present.
func hexToBytes(s string) ([]byte, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	return hex.DecodeString(s)
}

// addressBytes parses an EVM address (case-insensitive on the hex
// alphabet, ignoring EIP-55 checksum) into the 20-byte canonical form.
// Returns an error if the input isn't exactly 20 bytes after hex
// decoding.
func addressBytes(addr string) ([]byte, error) {
	b, err := hexToBytes(addr)
	if err != nil {
		return nil, fmt.Errorf("address hex decode: %w", err)
	}
	if len(b) != 20 {
		return nil, fmt.Errorf("address must be 20 bytes (got %d)", len(b))
	}
	return b, nil
}

// uint256Bytes parses a decimal-string uint256 (the wire form for all
// numeric x402 fields) into its 32-byte big-endian ABI encoding. Returns
// an error if the value is negative or doesn't fit in 256 bits.
func uint256Bytes(decimal string) ([]byte, error) {
	n, ok := new(big.Int).SetString(decimal, 10)
	if !ok {
		return nil, fmt.Errorf("not a decimal integer: %q", decimal)
	}
	if n.Sign() < 0 {
		return nil, fmt.Errorf("negative uint256: %q", decimal)
	}
	b := n.Bytes()
	if len(b) > 32 {
		return nil, fmt.Errorf("uint256 overflow: %q", decimal)
	}
	return padLeft32(b), nil
}

// bytes32 parses a 32-byte hex value (e.g. EIP-3009 nonce) into raw
// bytes. Returns an error if the decoded length isn't exactly 32.
func bytes32(hex32 string) ([]byte, error) {
	b, err := hexToBytes(hex32)
	if err != nil {
		return nil, fmt.Errorf("bytes32 hex decode: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("bytes32 must be 32 bytes (got %d)", len(b))
	}
	return b, nil
}

// DomainSeparator computes keccak256(abi.encode(
//
//	EIP712Domain typehash, keccak256(name), keccak256(version),
//	chainId (padded), verifyingContract (padded)))
//
// per EIP-712 §3.
func DomainSeparator(name, version string, chainID uint64, verifyingContract string) ([]byte, error) {
	contractAddr, err := addressBytes(verifyingContract)
	if err != nil {
		return nil, fmt.Errorf("domain: verifyingContract: %w", err)
	}
	chainIDBig := new(big.Int).SetUint64(chainID)

	buf := make([]byte, 0, 32*5)
	buf = append(buf, eip712DomainTypeHash...)
	buf = append(buf, keccak256([]byte(name))...)
	buf = append(buf, keccak256([]byte(version))...)
	buf = append(buf, padLeft32(chainIDBig.Bytes())...)
	buf = append(buf, padLeft32(contractAddr)...)
	return keccak256(buf), nil
}

// TransferWithAuthorizationStructHash computes the EIP-712 struct hash
// for an EIP-3009 transferWithAuthorization message — the inner block
// that gets domain-prefixed and hashed once more for the final digest.
func TransferWithAuthorizationStructHash(auth EVMAuthorizationV2) ([]byte, error) {
	from, err := addressBytes(auth.From)
	if err != nil {
		return nil, fmt.Errorf("auth: from: %w", err)
	}
	to, err := addressBytes(auth.To)
	if err != nil {
		return nil, fmt.Errorf("auth: to: %w", err)
	}
	value, err := uint256Bytes(auth.Value)
	if err != nil {
		return nil, fmt.Errorf("auth: value: %w", err)
	}
	validAfter, err := uint256Bytes(auth.ValidAfter)
	if err != nil {
		return nil, fmt.Errorf("auth: validAfter: %w", err)
	}
	validBefore, err := uint256Bytes(auth.ValidBefore)
	if err != nil {
		return nil, fmt.Errorf("auth: validBefore: %w", err)
	}
	nonce, err := bytes32(auth.Nonce)
	if err != nil {
		return nil, fmt.Errorf("auth: nonce: %w", err)
	}

	buf := make([]byte, 0, 32*7)
	buf = append(buf, transferWithAuthorizationTypeHash...)
	buf = append(buf, padLeft32(from)...)
	buf = append(buf, padLeft32(to)...)
	buf = append(buf, value...)
	buf = append(buf, validAfter...)
	buf = append(buf, validBefore...)
	buf = append(buf, nonce...)
	return keccak256(buf), nil
}

// EIP3009Digest combines the EIP-712 domain separator with the
// transferWithAuthorization struct hash to produce the final 32-byte
// digest the client signs.
//
// The 0x1901 prefix is fixed by EIP-712 (\x19\x01); it disambiguates
// signed-typed-data from other EVM signature surfaces (e.g. personal
// messages use \x19Ethereum Signed Message: and ECDSA-over-arbitrary-
// hashes uses no prefix at all).
func EIP3009Digest(chain *ChainInfo, auth EVMAuthorizationV2) ([]byte, error) {
	domainSep, err := DomainSeparator(chain.EIP712Name, chain.EIP712Version, chain.ChainID, chain.USDCAddress)
	if err != nil {
		return nil, err
	}
	structHash, err := TransferWithAuthorizationStructHash(auth)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, 2+32+32)
	buf = append(buf, 0x19, 0x01)
	buf = append(buf, domainSep...)
	buf = append(buf, structHash...)
	return keccak256(buf), nil
}
