package auth

// The demo accounts.
//
// These exist so the role separation can be tried out — signed in as each one
// in turn, seeing which buttons appear and which postings are refused. They
// are fixtures, not credentials: the password below is in the source, in the
// documentation, and on the login screen of any deployment that has them.
//
// That is only acceptable because a database holding them is marked as such.
// Accounts created here carry is_demo, the server warns about them on every
// start, and `spp-seed-users -remove-demo` takes them away again.
//
// They live in this package rather than in the seeding command because two
// other places need them: the login screen, which offers them, and the tests,
// which sign in as them.
const (
	// DemoPassword is shared by every demo account, so that trying the system
	// as four different people does not mean looking up four passwords.
	//
	// One password across several accounts is exactly what nobody should do
	// with real ones. It is right here for the same reason it is wrong there:
	// these accounts are meant to be used by anyone who has the page open.
	DemoPassword = "Demo-Sugar-2027"
)

// DemoAccount describes one fixture account.
type DemoAccount struct {
	Username string   `json:"username"`
	Name     string   `json:"displayName"`
	Email    string   `json:"email,omitempty"`
	Roles    []string `json:"roles"`
	// Remark says what signing in as this account demonstrates.
	Remark string `json:"remark"`
}

// DemoAccounts is the set created by `spp-seed-users -demo`, in the order a
// login screen should offer them: most able first.
var DemoAccounts = []DemoAccount{
	{
		Username: "admin", Name: "System Administrator",
		Email: "admin@demo.kss.local", Roles: []string{RoleAdmin},
		Remark: "Everything, including user administration and forcing a posting that capacity validation blocked.",
	},
	{
		Username: "planner", Name: "Production Planner",
		Email: "planner@demo.kss.local", Roles: []string{RolePlanner},
		Remark: "Maintains master data and the daily storage plan. Cannot post stock.",
	},
	{
		Username: "warehouse", Name: "Warehouse Supervisor",
		Email: "warehouse@demo.kss.local", Roles: []string{RoleWarehouse},
		Remark: "Posts receipts, issues and reservations. Cannot change master data.",
	},
	{
		Username: "refinery", Name: "Refinery Shift Lead",
		Email: "refinery@demo.kss.local", Roles: []string{RoleWarehouse, RolePlanner},
		Remark: "Two roles at once: moves stock and maintains the plan.",
	},
	{
		Username: "viewer", Name: "Management Viewer",
		Email: "viewer@demo.kss.local", Roles: []string{RoleViewer},
		Remark: "Reads every screen. Every write is refused.",
	},
}
