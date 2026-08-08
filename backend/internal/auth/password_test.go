package auth

import (
	"bytes"
	"strings"
	"testing"
)

func TestCheckPasswordPolicy(t *testing.T) {
	cases := []struct {
		name     string
		username string
		password string
		accepted bool
	}{
		{"a reasonable password", "planner", "Demo-Planner-2027", true},
		{"letters and a digit", "planner", "sugarcane7", true},
		{"too short", "planner", "sugar7", false},
		{"letters only", "planner", "sugarcanesugar", false},
		{"digits only", "planner", "1234567890", false},
		{"an obvious one", "planner", "Password123", false},
		{"the user name itself", "planner", "PLANNER", false},
		{"past what bcrypt reads", "planner", strings.Repeat("a1", 40), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckPasswordPolicy(c.username, c.password)
			if c.accepted && err != nil {
				t.Fatalf("expected %q to be accepted, got %v", c.password, err)
			}
			if !c.accepted && err == nil {
				t.Fatalf("expected %q to be rejected", c.password)
			}
		})
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	const password = "Demo-Warehouse-2027"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, password) {
		t.Fatal("the digest contains the password")
	}
	if !VerifyPassword(hash, password) {
		t.Fatal("the correct password was not accepted")
	}
	if VerifyPassword(hash, password+"x") {
		t.Fatal("a wrong password was accepted")
	}

	// The same password hashed twice must differ, or equal passwords would be
	// visible as equal digests.
	other, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if other == hash {
		t.Fatal("two digests of the same password are identical; the salt is not doing its job")
	}
}

func TestVerifyPasswordWithNoDigest(t *testing.T) {
	// An account with no password never signs in, whatever is offered —
	// including the empty string.
	if VerifyPassword("", "anything") {
		t.Fatal("an account with no digest accepted a password")
	}
	if VerifyPassword("", "") {
		t.Fatal("an account with no digest accepted an empty password")
	}
}

func TestNewSessionToken(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 40 {
		t.Fatalf("token is only %d characters; that is not 32 bytes of randomness", len(token))
	}
	if !bytes.Equal(hash, HashToken(token)) {
		t.Fatal("the returned hash is not the hash of the returned token")
	}
	if strings.Contains(string(hash), token) {
		t.Fatal("the stored hash contains the token")
	}

	second, _, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if second == token {
		t.Fatal("two tokens came back identical")
	}
}

func TestGeneratePasswordSatisfiesThePolicy(t *testing.T) {
	// A generated password that the policy would reject would make account
	// creation fail at random, which is the kind of bug that only shows up in
	// front of somebody.
	for i := 0; i < 200; i++ {
		p, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckPasswordPolicy("someone", p); err != nil {
			t.Fatalf("generated %q, which the policy rejects: %v", p, err)
		}
	}
}

func TestIdentityRoles(t *testing.T) {
	id := Identity{Subject: "planner", Roles: []string{"planner"}}

	// Identity providers disagree about casing, so the comparison must not.
	if !id.HasRole("PLANNER") {
		t.Fatal("role matching is case sensitive")
	}
	if id.HasRole("ADMIN") {
		t.Fatal("a role the user does not hold was reported as held")
	}
	if !id.HasAnyRole("ADMIN", "PLANNER") {
		t.Fatal("HasAnyRole missed a held role")
	}
	if id.HasAnyRole("ADMIN", "WAREHOUSE") {
		t.Fatal("HasAnyRole matched a role the user does not hold")
	}
	// No roles listed means the operation is open to anyone signed in.
	if !id.HasAnyRole() {
		t.Fatal("an unrestricted operation was refused")
	}
}

func TestParseModeAcceptsLocal(t *testing.T) {
	for _, in := range []string{"local", "LOCAL", " local "} {
		got, ok := ParseMode(in)
		if !ok || got != ModeLocal {
			t.Fatalf("ParseMode(%q) = %v,%v want local,true", in, got, ok)
		}
	}
}

func TestKnownRoles(t *testing.T) {
	for _, r := range KnownRoles {
		if !IsKnownRole(strings.ToLower(r)) {
			t.Fatalf("%q is in KnownRoles but IsKnownRole says otherwise", r)
		}
	}
	// A role nobody enforces must be refused rather than stored, or an
	// administrator will grant it and believe it did something.
	if IsKnownRole("SUPERVISOR") {
		t.Fatal("an unenforced role was accepted as known")
	}
}
