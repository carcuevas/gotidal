package main

import (
	"fmt"

	"github.com/carcuevas/gotidal/internal/store"
	"github.com/carcuevas/gotidal/internal/tidal"
)

func runLogout() {
	ctx, stop := signalContext()
	defer stop()

	vault := store.NewSecretsStore(readPassphrase)
	defer vault.Close()

	// Load the session so both tokens can be revoked server-side. The refresh
	// token matters most: it is the long-lived one, so revoking only the
	// access token would leave anyone holding a copy of the secrets store able
	// to mint fresh access tokens long after the user believed they had
	// logged out.
	var session tidal.Session
	if err := vault.LoadSession(&session); err == nil {
		client := tidal.NewClient()
		for _, tok := range []struct{ kind, value string }{
			{"refresh token", session.RefreshToken},
			{"access token", session.AccessToken},
		} {
			if tok.value == "" {
				continue
			}
			if err := client.RevokeToken(ctx, tok.value); err != nil {
				fmt.Printf("Warning: failed to revoke %s: %v\n", tok.kind, err)
			}
		}
	}

	if err := vault.DeleteSession(); err != nil {
		fatalf("logout: %v", err)
	}
	fmt.Println("Logged out. Run `gotidal` to log in again.")
}
