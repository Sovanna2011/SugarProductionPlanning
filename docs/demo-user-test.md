# Demo system — user test

A script for putting the system in front of the people who would use it, and
writing down what happens.

The point is not to confirm it works. It is to find the places where it does
the right thing in a way nobody expects, or asks for something the person on
the shift floor does not have. Those only surface when somebody who did not
build it tries to get something done.

## Starting it

**If all you want is a link somebody can click:** `system.html` on the
published pages —
<https://sovanna2011.github.io/SugarProductionPlanning/system.html> — is the
system running in the browser, with the same accounts, the same roles, the same
capacity rules and the same season plan. Nothing to install, nothing to start,
works on a phone. Its limit is that it has no server: each person gets their
own copy, so it cannot show two people working on the same data, and nothing
anybody does is recorded anywhere you can read afterwards. For a first
walk-through with one person at a time it is enough, and it is by far the
fastest thing to hand somebody.

**For anything involving more than one person**, or where you want to look at
what was posted afterwards, run the real system. The quickest way to get a URL
somebody else can open is a Codespace on this repository — it installs nothing on your machine and gives the running system
an HTTPS address:

<https://codespaces.new/Sovanna2011/SugarProductionPlanning>

The Codespace makes that address public and prints it, so the people you are
testing with can just open it — no GitHub account, nothing to install. It is
genuinely public: no permission check, on accounts whose password is published
in this repository. Fine for an afternoon of user testing on invented data,
not fine once the database holds anything real; see the last section. Take it
back down with `./scripts/share.sh private`.

On your own machine:

```bash
docker compose -f docker-compose.demo.yml up --build     # then open http://localhost:8080
```

Or, on a machine with Go and PostgreSQL but no Docker:

```bash
./scripts/demo.sh
```

Either way you get the 2026/27 season plan, the stock position the plan itself
predicts for 15 Feb 2027, and five accounts. Everything is safe to repeat — a
restart neither duplicates the data nor fails.

Use `--date 2027-04-10` (or `SPP_DEMO_DATE=2027-04-10`) to start from the raw
sugar peak instead: 109,720 t against 110,000 t of capacity. It is the more
interesting day, because the system is nearly full.

## The accounts

All five use the password **`Demo-Sugar-2027`**, which the login screen also
lists. Click an account there and it signs you straight in.

| Sign in as | Is | Should be able to |
|---|---|---|
| `viewer` | Management Viewer | Read every screen, change nothing |
| `warehouse` | Warehouse Supervisor | Move stock; not touch master data |
| `planner` | Production Planner | Maintain master data and the plan; not move stock |
| `refinery` | Refinery Shift Lead | Both of the above |
| `admin` | System Administrator | Everything, plus users and capacity overrides |

## What to ask people to do

Give each person the tasks for their role and **watch without helping**. Where
they hesitate is the finding; what they say afterwards is commentary.

### Everyone, whichever account

1. Sign in. **Without being told**, say which storage is closest to full.
2. Find how much room is left in the raw sugar warehouses, in tons and in bags.
3. Find the day the finished sugar warehouses are expected to overflow.
   *(The system says 28 May 2027. Did they find it, and did they believe it?)*
4. Sign out and back in as somebody else. Say what changed.

### As `warehouse`

5. Open **Actual against plan**, pick a day nobody has reported, and record what
   was actually made and shipped. **Then say where the actual figures came
   from.** *(They are added up from the movements the recording posted, and
   those movements are listed on the same screen. If somebody believes they
   typed a number into an actual column, ask them to find that column.)*
6. Find a day where the actual closing stock does not match the plan and say
   why. *(One reason is always there: sugar is packed in whole bags, so a
   900 t plan lands at 899.6 t. Is that a problem to fix or a fact to
   accept? Somebody on site knows.)*
7. Receive 500 t of raw sugar in bulk into Raw Warehouse 1.
8. Receive enough to push a warehouse past its safe level. **What does the
   system tell you, and is it clear whether the posting went through?**
   *(It should post, with a warning. Above physical capacity it should refuse.)*
9. Issue raw sugar to remelt from a specific batch.
10. Try to change a warehouse's capacity. *(Should be refused — is the refusal
   understandable, or does it look like a fault?)*

### As `planner`

11. Correct a molasses tank's capacity to whatever it really is.
   *(The seeded 5,000 t is a placeholder from the requirement document; this is
   the task where somebody who knows the site can tell us the real number.)*
12. Add a new packaging size and give a warehouse a ceiling for it.
13. Change the alert bands so "nearly full" means something the factory agrees
    with.
14. Open the daily production plan and change one day — say, raise quota sales
    for 16 Feb 2027. **Then say what it did to the date the finished sugar
    warehouses overflow.** *(It should move: the pool figures follow from the
    production figures, so a change to one is a change to the other. If the
    person cannot tell that anything happened, the screen has failed.)*
15. Work out how much has to be sold each day for the season to end inside
    capacity. *(About 640 t/day holds the closing stock under 69,000 t; about
    890 t/day clears the whole season's production. Do they arrive at
    something near that, and how?)*
16. Try to post a stock receipt. *(Should be refused.)*

### As `admin`

17. Create an account for a real colleague with the right roles, and read out
    the password it generates.
18. Reset somebody's password and sign them out everywhere.
19. Force a posting that capacity validation blocked, giving a reason. Then
    find that reason again afterwards.
20. Try to remove your own administrator role. *(Should be refused if you are
    the last one — is the reason clear?)*

### Which of these the browser-only page cannot do

Tasks 1–11, 14–16 and 20 work in `system.html`. The rest need the real system:

- **12** — adding a packaging size. The browser page shows the packaging master
  and every ceiling, but only storage locations are editable in it.
- **13** — changing the alert bands.
- **18** — resetting a password and signing somebody out everywhere. One
  browser holds one session, so there is nowhere else to sign out of.
- **17** works, except that you type the new password rather than being given a
  generated one.
- **19** works, but the reason is only findable again in that same browser.

If a session is going to cover those, run the real system for it.

## What to write down

For each task: **did they finish it, how long did it take, and what did they
try first?** The first thing somebody reaches for is worth more than their
opinion of the screen afterwards.

Then the questions the software cannot answer:

- **Are the placeholder capacities right?** The molasses tanks (5,000 t each),
  the conditioning silo (2,000 t) and the product/packaging splits are
  illustrative figures from the requirement document, marked `EXAMPLE` in every
  row's remark. Somebody on site knows the real numbers.
- **Should Finished Sugar Warehouse 2 be active?** It is in the requirement's
  storage structure but not in the plan, so it is seeded inactive with zero
  capacity rather than given an invented figure. Activating it is the most
  direct answer to the finished-goods shortfall.
- **Are the alert bands right?** 95% currently means "critical". If the
  factory acts at 90%, the system should say so at 90%.
- **Do the roles match the jobs?** Four roles are a guess at how the site is
  organised. If the person who maintains the plan also posts stock, that is
  two roles on one account today, and might be a role of its own.

## Before anyone real uses it

The demo database must not become the production one. It holds accounts whose
password is published in this repository, and the server says so on every
start.

```bash
docker compose -f docker-compose.demo.yml exec app spp-seed-users -remove-demo
docker compose -f docker-compose.demo.yml exec app spp-seed-users -admin <name>
```

The second command prints a generated password once and requires it to be
changed at first sign-in. Then read [`security.md`](security.md) — in
particular `SPP_SESSION_COOKIE_SECURE`, the database password, and which
authentication mode the site should run.
