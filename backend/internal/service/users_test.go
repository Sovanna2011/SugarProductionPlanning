package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
)

func TestUserInputValidate(t *testing.T) {
	valid := func() UserInput {
		return UserInput{
			Username:    "Warehouse",
			DisplayName: "Warehouse Supervisor",
			Email:       "warehouse@example.com",
			Roles:       []string{"warehouse"},
		}
	}

	t.Run("normalises what it accepts", func(t *testing.T) {
		in := valid()
		if err := in.Validate(); err != nil {
			t.Fatal(err)
		}
		// The name is the audit subject, so two spellings must not become two
		// accounts.
		if in.Username != "warehouse" {
			t.Fatalf("username %q was not folded to lower case", in.Username)
		}
		if in.Roles[0] != auth.RoleWarehouse {
			t.Fatalf("role %q was not folded to upper case", in.Roles[0])
		}
		if in.Status != "ACTIVE" {
			t.Fatalf("status defaulted to %q", in.Status)
		}
	})

	t.Run("refuses a role nothing enforces", func(t *testing.T) {
		in := valid()
		in.Roles = []string{"SUPERVISOR"}
		err := in.Validate()
		if err == nil {
			t.Fatal("an unenforced role was stored, which grants nothing but looks like it does")
		}
		if !strings.Contains(err.Error(), auth.RoleWarehouse) {
			t.Fatalf("the message does not say what the roles are: %v", err)
		}
	})

	t.Run("refuses an account with no role", func(t *testing.T) {
		in := valid()
		in.Roles = nil
		if err := in.Validate(); err == nil {
			t.Fatal("an account with no role at all was accepted")
		}
	})

	t.Run("drops duplicates and sorts", func(t *testing.T) {
		in := valid()
		in.Roles = []string{"PLANNER", "warehouse", "Planner"}
		if err := in.Validate(); err != nil {
			t.Fatal(err)
		}
		if len(in.Roles) != 2 || in.Roles[0] != auth.RolePlanner || in.Roles[1] != auth.RoleWarehouse {
			t.Fatalf("roles came out as %v", in.Roles)
		}
	})

	t.Run("insists on a display name", func(t *testing.T) {
		in := valid()
		in.DisplayName = "  "
		if err := in.Validate(); err == nil {
			t.Fatal("an account with no display name was accepted, so the audit trail would name nobody")
		}
	})

	t.Run("checks the user name only on create", func(t *testing.T) {
		in := valid()
		in.Username = "ab"
		if err := in.Validate(); err == nil {
			t.Fatal("a two-character user name was accepted")
		}

		// On update the name is not editable, so a short one already in the
		// database must not block an otherwise valid change.
		in = valid()
		in.ID, in.Username, in.Version = 7, "ab", 3
		if err := in.Validate(); err != nil {
			t.Fatalf("an update was refused over a name it cannot change: %v", err)
		}
	})

	t.Run("rejects a weak initial password", func(t *testing.T) {
		in := valid()
		in.Password = "sugar"
		err := in.Validate()
		if err == nil {
			t.Fatal("a five-character password was accepted")
		}
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("want a validation error, got %v", err)
		}
		// The rule has to reach the person typing it.
		if !strings.Contains(err.Error(), "10 characters") {
			t.Fatalf("the message does not state the rule: %v", err)
		}
	})

	t.Run("rejects an address that is not one", func(t *testing.T) {
		in := valid()
		in.Email = "warehouse.example.com"
		if err := in.Validate(); err == nil {
			t.Fatal("an email address without an @ was accepted")
		}
	})

	t.Run("rejects an unknown status", func(t *testing.T) {
		in := valid()
		in.Status = "SUSPENDED"
		if err := in.Validate(); err == nil {
			t.Fatal("a status the database would refuse was accepted here")
		}
	})
}

func TestHasRoleHelper(t *testing.T) {
	roles := []string{"PLANNER", "WAREHOUSE"}
	if !hasRole(roles, "planner") {
		t.Fatal("role matching is case sensitive")
	}
	if hasRole(roles, auth.RoleAdmin) {
		t.Fatal("a role that is not held was reported as held")
	}
	if hasRole(nil, auth.RoleAdmin) {
		t.Fatal("an account with no roles was reported as an administrator")
	}
}
