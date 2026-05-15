package payment

import (
	"errors"
	"strings"
)

// ChainInfo carries the per-network constants needed to build an EIP-712
// `transferWithAuthorization` signature for that network's canonical
// USDC contract.
//
// v0.2 hardcodes a small allowlist; multi-token / arbitrary-asset support
// (where the resource server names whichever ERC-20 it likes and the
// daimon fetches the EIP-712 domain from the chain's RPC) is reserved for
// v0.2.x. Hardcoding keeps the v0.2 surface auditable — every signature
// the daimon emits is anchored to a contract address the user can verify
// against Circle's published deployments page.
//
// Sources:
//   - Base mainnet USDC:     https://www.circle.com/blog/usdc-on-base
//   - Base Sepolia USDC:     https://developers.circle.com/stablecoins/docs/usdc-on-test-networks
//   - EIP-712 domain shape:  https://eips.ethereum.org/EIPS/eip-712
//   - EIP-3009:              https://eips.ethereum.org/EIPS/eip-3009
type ChainInfo struct {
	// X402Network is the network identifier as it appears in the wire
	// PaymentRequirements.Network field. x402 uses bare names like "base"
	// rather than the EVM CAIP-2 "eip155:8453" form.
	X402Network string

	// DaimonChain is the chain label used internally by the wallet store
	// (e.g. "evm:base"). The wallet keystore tags entries with this label
	// when CreateWallet is called; payment.ExecutePaidRequest maps
	// x402's network back to this for signing.
	DaimonChain string

	// ChainID is the EVM chain id used in EIP-712 domain separators.
	ChainID uint64

	// USDCAddress is the EIP-55-checksummed contract address of the
	// canonical USDC deployment on this chain. The daimon will only sign
	// PaymentRequirements whose Asset field equals this address (case
	// insensitive comparison).
	USDCAddress string

	// EIP712Name is the `name` field of USDC's EIP-712 domain on this
	// chain. Per the v2 FiatTokenV2 contract this is always "USD Coin"
	// for circle-issued USDC, but bridged variants on testnets sometimes
	// vary — hence per-chain rather than a global constant.
	EIP712Name string

	// EIP712Version is the `version` field of USDC's EIP-712 domain.
	// Circle's v2 contracts use "2".
	EIP712Version string
}

// Registered chains. Add by appending; the lookup is linear.
var registeredChains = []ChainInfo{
	{
		X402Network:   "base",
		DaimonChain:   "evm:base",
		ChainID:       8453,
		USDCAddress:   "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		EIP712Name:    "USD Coin",
		EIP712Version: "2",
	},
	{
		X402Network:   "base-sepolia",
		DaimonChain:   "evm:base-sepolia",
		ChainID:       84532,
		USDCAddress:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		EIP712Name:    "USDC",
		EIP712Version: "2",
	},
}

// ErrUnknownNetwork is returned by LookupByX402Network when the network
// identifier in a PaymentRequirements row doesn't match any of v0.2's
// supported chains.
var ErrUnknownNetwork = errors.New("payment: unknown x402 network")

// LookupByX402Network resolves a wire network identifier ("base",
// "base-sepolia") to its ChainInfo. Lookup is case-insensitive on the
// network name to match how some servers emit the field.
func LookupByX402Network(network string) (*ChainInfo, error) {
	n := strings.ToLower(network)
	for i := range registeredChains {
		if registeredChains[i].X402Network == n {
			return &registeredChains[i], nil
		}
	}
	return nil, ErrUnknownNetwork
}

// LookupByDaimonChain resolves a wallet-store chain label ("evm:base")
// to its ChainInfo. Used when the caller chooses a wallet and wants the
// matching x402 network identifier for outbound requests.
func LookupByDaimonChain(chain string) (*ChainInfo, error) {
	for i := range registeredChains {
		if registeredChains[i].DaimonChain == chain {
			return &registeredChains[i], nil
		}
	}
	return nil, ErrUnknownNetwork
}
