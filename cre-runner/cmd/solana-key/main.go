// solana-key prints a solana-keygen keypair file as the base58 secret key that
// CRE_SOLANA_PRIVATE_KEY accepts (Fly secrets can't hold files), and its public key on stderr.
//
// Usage: go run ./cmd/solana-key ~/.config/solana/id.json
package main

import (
	"fmt"
	"os"

	"github.com/token2049-decentralizing/core/cre-runner/internal/solana"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: solana-key <keypair.json>")
		os.Exit(2)
	}
	key, err := solana.ParsePrivateKey(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "public key:", key.PublicKey())
	fmt.Print(key.String())
}
