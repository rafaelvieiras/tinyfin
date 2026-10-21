package sqlite

import (
	"context"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// A read-modify-write of an unrelated member (policy, name, last login) must
// not bring back a password hash that was replaced in between.
func TestUpdateUserKeepsConcurrentPasswordChange(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	id, err := st.CreateUser(ctx, storage.User{Name: "pw-race", PasswordHash: "old"})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := st.GetUser(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserPasswordHash(ctx, id, "new"); err != nil {
		t.Fatal(err)
	}
	stale.IsDisabled = true
	if err := st.UpdateUser(ctx, stale); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetUser(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash != "new" || !got.IsDisabled {
		t.Fatalf("hash = %q disabled = %v, want the new hash and the profile change", got.PasswordHash, got.IsDisabled)
	}
	if err := st.SetUserPasswordHash(ctx, "missing", "x"); err == nil {
		t.Fatal("setting the hash of an unknown user must fail")
	}
}
