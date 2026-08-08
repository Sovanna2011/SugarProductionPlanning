// Command seed-users creates the accounts a login-enabled deployment needs.
//
// Accounts are not created by a migration on purpose. A migration runs on
// every deployment, so seeding accounts there would give every installation
// the same known passwords, and there would be no moment at which somebody
// decided that was acceptable. Here there is: you run this.
//
// Two ways to use it:
//
//	spp-seed-users -admin sovanna -name "Hang Sovanna"
//	    One administrator with a generated password, printed once. This is what
//	    a real deployment wants.
//
//	spp-seed-users -demo
//	    One account per role with the passwords published in docs/security.md,
//	    so the role behaviour can be tried out. These are flagged in the
//	    database and the server warns about them on every start.
//
//	spp-seed-users -remove-demo
//	    Deactivates the demo accounts and ends their sessions.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// demoAccount is one of the accounts -demo creates.
//
// The passwords are here in the source, and that is the point: they are not
// secrets, they are fixtures. They are long enough to satisfy the password
// policy so that the accounts exercise the same path a real one would.
type demoAccount struct {
	username string
	name     string
	email    string
	roles    []string
	password string
	remark   string
}

var demoAccounts = []demoAccount{
	{
		username: "admin", name: "System Administrator",
		email: "admin@demo.kss.local",
		roles: []string{auth.RoleAdmin}, password: "Demo-Admin-2027",
		remark: "Everything, including user administration and forcing a blocked posting.",
	},
	{
		username: "planner", name: "Production Planner",
		email: "planner@demo.kss.local",
		roles: []string{auth.RolePlanner}, password: "Demo-Planner-2027",
		remark: "Maintains master data and the daily storage plan. Cannot post stock.",
	},
	{
		username: "warehouse", name: "Warehouse Supervisor",
		email: "warehouse@demo.kss.local",
		roles: []string{auth.RoleWarehouse}, password: "Demo-Store-2027",
		remark: "Posts receipts, issues and reservations. Cannot change master data.",
	},
	{
		username: "refinery", name: "Refinery Shift Lead",
		email: "refinery@demo.kss.local",
		roles: []string{auth.RoleWarehouse, auth.RolePlanner}, password: "Demo-Refinery-2027",
		remark: "Two roles at once: moves stock and maintains the plan.",
	},
	{
		username: "viewer", name: "Management Viewer",
		email: "viewer@demo.kss.local",
		roles: []string{auth.RoleViewer}, password: "Demo-Viewer-2027",
		remark: "Reads every screen. Every write is refused.",
	},
}

func main() {
	var (
		dsn         = flag.String("database-url", os.Getenv("SPP_DATABASE_URL"), "PostgreSQL connection string")
		demo        = flag.Bool("demo", false, "create one account per role with published demo passwords")
		removeDemo  = flag.Bool("remove-demo", false, "deactivate the demo accounts and end their sessions")
		adminName   = flag.String("admin", "", "create an administrator with this user name")
		displayName = flag.String("name", "", "display name for -admin")
		password    = flag.String("password", "", "password for -admin (one is generated if omitted)")
		reset       = flag.Bool("reset", false, "reset the password of an account that already exists")
	)
	flag.Parse()

	if *dsn == "" {
		log.Fatal("set SPP_DATABASE_URL or pass -database-url")
	}
	if !*demo && !*removeDemo && *adminName == "" {
		flag.Usage()
		log.Fatal("nothing to do: pass -demo, -remove-demo or -admin <name>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	store, err := postgres.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer store.Close()

	switch {
	case *removeDemo:
		if err := deactivateDemo(ctx, store); err != nil {
			log.Fatal(err)
		}
	case *demo:
		if err := seedDemo(ctx, store, *reset); err != nil {
			log.Fatal(err)
		}
	}

	if *adminName != "" {
		if err := seedAdmin(ctx, store, *adminName, *displayName, *password, *reset); err != nil {
			log.Fatal(err)
		}
	}
}

func seedDemo(ctx context.Context, store *postgres.Store, reset bool) error {
	fmt.Println("Creating demo accounts. Their passwords are published in docs/security.md,")
	fmt.Println("so this database must not be used for anything real.")
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "USER\tPASSWORD\tROLES\tWHAT IT CAN DO")

	for _, a := range demoAccounts {
		// A demo account is created with the password already accepted, not
		// flagged for change: the point is to sign in as it and use the
		// system, and a forced password change on every login would make the
		// fixture useless.
		digest, err := auth.HashPassword(a.password)
		if err != nil {
			return err
		}

		existing, err := store.GetUserByUsername(ctx, a.username)
		switch {
		case err == nil && !reset:
			fmt.Fprintf(w, "%s\t(unchanged)\t%s\t%s\n", a.username, strings.Join(existing.Roles, " "), a.remark)
			continue
		case err == nil:
			if err := store.SetPassword(ctx, existing.ID, digest, false, "seed-users"); err != nil {
				return err
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.username, a.password, strings.Join(existing.Roles, " "), a.remark)
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		created, err := store.InsertUser(ctx, domain.User{
			Username:    a.username,
			DisplayName: a.name,
			Email:       a.email,
			Roles:       a.roles,
			Status:      domain.StatusActive,
			IsDemo:      true,
			Remark:      a.remark,
		}, digest, "seed-users")
		if err != nil {
			return fmt.Errorf("create %s: %w", a.username, err)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", created.Username, a.password, strings.Join(created.Roles, " "), a.remark)
	}

	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Start the server with SPP_AUTH_MODE=local to sign in with these.")
	fmt.Println("Remove them again with: spp-seed-users -remove-demo")
	return nil
}

func deactivateDemo(ctx context.Context, store *postgres.Store) error {
	users, err := store.ListUsers(ctx, postgres.UserFilter{IncludeInactive: true})
	if err != nil {
		return err
	}

	var n int
	for _, u := range users {
		if !u.IsDemo || u.Status != domain.StatusActive {
			continue
		}
		u.Status = domain.StatusInactive
		if _, err := store.UpdateUser(ctx, u, "seed-users"); err != nil {
			return fmt.Errorf("deactivate %s: %w", u.Username, err)
		}
		if _, err := store.RevokeUserSessions(ctx, u.ID); err != nil {
			return err
		}
		fmt.Printf("  %s deactivated and signed out\n", u.Username)
		n++
	}

	if n == 0 {
		fmt.Println("No active demo accounts.")
		return nil
	}
	fmt.Printf("\n%d demo account(s) deactivated. Their history is kept, so the audit trail\n", n)
	fmt.Println("still names them; they simply cannot sign in.")
	return nil
}

func seedAdmin(ctx context.Context, store *postgres.Store, username, display, password string, reset bool) error {
	username = strings.ToLower(strings.TrimSpace(username))
	if display == "" {
		display = username
	}

	generated := password == ""
	if generated {
		var err error
		if password, err = auth.GeneratePassword(); err != nil {
			return err
		}
	}
	if err := auth.CheckPasswordPolicy(username, password); err != nil {
		return err
	}
	digest, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	existing, err := store.GetUserByUsername(ctx, username)
	switch {
	case err == nil && !reset:
		return fmt.Errorf("%s already exists; pass -reset to give it a new password", username)
	case err == nil:
		// A password an administrator typed for somebody else is flagged for
		// change at first sign-in, whoever they are.
		if err := store.SetPassword(ctx, existing.ID, digest, true, "seed-users"); err != nil {
			return err
		}
		fmt.Printf("Password reset for %s.\n", username)
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := store.InsertUser(ctx, domain.User{
			Username:           username,
			DisplayName:        display,
			Roles:              []string{auth.RoleAdmin},
			Status:             domain.StatusActive,
			MustChangePassword: true,
			Remark:             "Created by spp-seed-users",
		}, digest, "seed-users"); err != nil {
			return fmt.Errorf("create %s: %w", username, err)
		}
		fmt.Printf("Administrator %s created.\n", username)
	default:
		return err
	}

	if generated {
		fmt.Printf("\n  password: %s\n\n", password)
		fmt.Println("This is the only time it is shown. It must be changed at first sign-in.")
	} else {
		fmt.Println("It must be changed at first sign-in.")
	}
	return nil
}
