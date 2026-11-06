package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aliking1367/next/internal/app/nodecontroller"
	settingsapp "github.com/aliking1367/next/internal/app/settings"
)

// The switch starts on, a panel that turns it off keeps it off, and the node
// configs read the same value.
func TestAbuseProtectionStartsOnAndCanBeTurnedOff(t *testing.T) {
	server, _ := testBotMigratedServer(t)
	ctx := context.Background()
	repo := nodecontroller.NewRepository(server.db, server.dialect)
	settingsRepo := settingsapp.NewRepository(server.db, server.dialect)

	settings, err := settingsRepo.RuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.AbuseProtection || !repo.AbuseProtection(ctx) {
		t.Fatalf("abuse protection should start on: settings=%v repo=%v", settings.AbuseProtection, repo.AbuseProtection(ctx))
	}
	off, _ := json.Marshal(false)
	if _, err := settingsRepo.UpdateRuntimeSettings(ctx, map[string]json.RawMessage{"abuse_protection": off}); err != nil {
		t.Fatal(err)
	}
	if repo.AbuseProtection(ctx) {
		t.Fatal("the node configs still see it on")
	}
}
