# What changed in each version

Newest first. Every version stays under **Releases** on GitHub with its `DrishtiMap.exe`; old versions
are never removed, so anyone can go back to an earlier one. Each heading says when the version was
released and who released it.

To publish a version: change `Version` in `app\app.go`, add a section at the top of this list as
`## <version> · <date> · by <your GitHub name>`, commit, then push a tag `v<version>`. GitHub builds the
exe and publishes the release by itself, with your name and the list of commits on the release page.

## 1.8.2 · 2026-11-20 · by yashwanta

- **Test connection explains a failure.** When the chosen model does not answer, the test tries it
  again without tools (to tell "this model cannot use tools" from "this model is down at the server"),
  then tries the connection's other models and lists the ones that answer with tools right now.
- Requests to OpenAI-compatible servers are a little more forgiving in how the tools are described.

## 1.8.1 · 2026-11-20 · by yashwanta

- **An AI connection can offer several models.** The Assistant's model list shows one entry per model,
  and marks a connection that has no key yet.
- **A site file can bring AI connections** for the Assistant (address and model names, never a key).
  They are taken in at every start from the site file beside the program, and when a site is joined.
  **Share this site** includes your own added connections, without their keys.

## 1.8.0 · 2026-11-20 · by yashwanta

- **Join a site with nothing but the server address.** The first step of the Guided tab has a "This
  site" card. Press **Join a site** and give the address of the plant's AMR Health server (or open a
  site file). The program asks the server which plant it is, fills in the robots, and copies the
  fleet's current map from a robot. No key, no password, no folders to prepare.
- **It joins by itself** when a site file (`drishti-site.json`) lies beside `DrishtiMap.exe`, when the
  variable `DRISHTI_AMRHEALTH_URL` is set, or when the company DNS has a `_drishti-amrhealth._tcp`
  record. A package of the exe plus the site file therefore works on the first double-click.
- **Share this site** writes that small site file for a colleague. It holds the server address and
  the plant name, no password.
- **Steps 1 to 3 work without a fleet manager login.** With no login set, or when the fleet manager
  cannot be read from this PC, the area report comes from AMR Health: which robots lost confidence at
  the stations in the box, their alarms, and whether it got worse after a map change. It is the quick
  view: by station and once a minute, so it does not count the passes that went well. The
  second-by-second view is used whenever a fleet manager login works.
- The checklist on the card shows what works (server, robots, map, history, stored laser scans) and
  offers the one button that fixes each thing that does not.
- New tool for the assistant: `site_setup`.

## 1.7.0 · 2026-11-20 · by yashwanta

- **Watch several robots for a box.** In the Evidence step, tick the robots (all are ticked to start
  with) and press **Record them when they drive through**. Each robot is recorded the first time it comes
  near the box, and its recording stops by itself. You do not have to know which robot will come by.
  The step shows what each robot is doing, and the watch can be stopped at any time; recordings made so
  far are kept.
- New tools for the assistant: `record_arm_all`, `record_arm_status`, `record_arm_stop`.

## 1.6.4 · 2026-11-20 · by yashwanta

- When the AMR Health server is restarting, the Evidence step now says so and asks to try again in a
  few minutes, instead of "answered 502".

## 1.6.3 · 2026-11-20 · by yashwanta

- Evidence step: when **Fix options** cannot be pressed yet, the step says why (no laser evidence for
  the box) and what to do. The nearest stored scan is now named correctly.

## 1.6.2 · 2026-11-20 · by yashwanta

- Stored laser scans are asked for **without a sign-in first**. A sign-in is used only when the AMR
  Health server itself requires one for its scans. On a server that offers them to the plant network,
  nothing has to be entered.

## 1.6.1 · 2026-11-20 · by yashwanta

- Stored scans: when none was taken near the box, the Evidence step now says how many scans AMR Health
  holds, where the newest were taken, and how far the nearest one is from the box, instead of only "none".
- Stored scans that fit the map poorly (20 to 40 % of points on walls) are no longer dropped when they stay
  where the robot said it was: a poor fit is the very thing to look at. They are counted and marked.
- When no stored scan can be placed, the message gives the best fit found and the robot's own confidence.

## 1.6.0 · 2026-11-20 · by yashwanta

- **Laser scans recorded automatically are read from the apps server.** When the workspace names an
  AMR Health server (version 0.7.74 or newer), the Evidence step looks there by itself for laser scans
  taken near the box: per robot how many there are, how many are usable, and how many were taken at
  low confidence.
- **Compare the stored scans with the map**: each scan is placed on the map (starting from the stored
  position and fitting it to the walls), then the step shows which walls were seen, what is seen that is
  not in the map, and **each robot's match against the map**. A robot clearly below the others is
  flagged for its scanners.
- **Sign in once.** The AMR Health user name and password are asked the first time and kept encrypted
  for the Windows user, like the AI keys. They are sent only to the server named in the workspace.
- A single stored scan cannot show a position jump or a misplaced piece; the step says so and still
  asks for a recorded drive for that.
- New tools for the assistant: `stored_scans`, `scan_evidence`.

## 1.5.0 · 2026-11-20 · by yashwanta

- **Evidence step rebuilt.** It now says what it is for, lists only the recordings that really drive
  through the box (passes, direction, lowest confidence) and says when none does.
- **Record it when it drives through**: choose a robot and the program records it by itself from the
  moment it comes near the box until it has left. Nobody has to time a start and a stop. The robot is
  still sent by the fleet manager; the program never moves a robot.
- **Compare what the robots see with the map**: map versions with the same walls in the box are grouped,
  so only versions that really differ there are offered, and a sensible few are ticked for you.
- **Move a whole misplaced piece, guided** (the method that fixed the reference case): the Evidence step
  now reports position jumps; when there is one, Fix options offers the piece move. The program finds
  the rigid pieces, you confirm which part is right, it measures the turn with its spread, you mark the
  piece on the map, and it builds, checks and replays. If jumps are left at the seam, it finds the map
  version whose walls fit that stretch best and repairs it. Going on to the trial needs all checks
  passed and no jump left.
- New tools for the assistant: `record_arm`, `box_sources`.

## 1.4.0 · 2026-11-20 · by yashwanta

- **Guided tab**: one trouble spot in eight steps, from a box on the map to a fixed map that was tried on
  one AMR and rolled out. It is now the first tab; the tools from before are under **Advanced**.
- **Step 1 to 3, area report**: draw a box and pick a time. The app reads what every robot did there and
  gives a verdict: the place, one robot, one robot everywhere (its scanner), worse since a map change, or
  one-off events. The rating comes from the number of robots and passes. Patterns: direction of travel,
  time of day, before and after each map change.
- **Step 4, laser evidence in a box**: how well each map version fits what the robots see, which walls
  were seen, and what is seen that is not in the map (in every recording, or only in some).
- **Step 5 and 6**: only fixes that fit the evidence can be chosen; the new file is checked and the same
  lasers are compared through the old and the new map. A box fix that would put a wall on a path is
  stopped, with the reason.
- **Step 7 and 8**: confirms the trial robot runs the new file, judges the trial recording (pass or
  fail), confirms the roll-out on every robot, and compares the box before and after.
- **Cases**: every investigation is saved in the workspace and listed on the left.
- Help: new work instruction WI-10.

## 1.3.2 · 2026-11-20 · by yashwanta

- Footer at the bottom of the window: the version, when it was released and by whom, and the build it
  came from. A copy built on a PC says "local build".
- **What changed** in the footer shows this list inside the program.
- Release pages now say who released the version and who made the changes in it, with the list of
  commits since the version before.

## 1.3.1 · 2026-11-20 · by yashwanta

- Releases are now built and published by GitHub from the source: every version tag gets its
  `DrishtiMap.exe` and zip under Releases, with this list of changes.
- Example names on the screens are neutral (`PLANTMAP_v5c` instead of a site's own map name).
- Tests read the reference maps' name from the workspace files, so no site name is written in the code.

## 1.3.0 · 2026-11-20 · by yashwanta

- **What happened?** (Tools tab, Past event): pick a robot and a time that has already passed. The app
  reads the fleet manager's history, read-only, and shows where the robot drove, every stop, position
  jump, relocalisation and error, whether the connection stalled, how every other robot did in the same
  place, and the Wi-Fi events of those minutes. The trip is drawn on the map, coloured by confidence.
- **History sources** dialog: fleet manager login and optional AMR Health server, kept per workspace.
- Help: new work instruction WI-09. The assistant can use the new tool.
- A test fails the build if the script that reads the fleet manager ever gains a call that writes,
  deletes, or starts another program.

## 1.2.1 · 2026-11-19 · by yashwanta

- Neutral wording in screens and help pages (no site names).

## 1.2.0 · 2026-11-17 · by yashwanta

- LiDAR scan viewer with the height of each scanner's scan plane, live or from a recording.
- AI model presets and a **Test connection** button in Settings.
- Logo and program icon.
- Hand **Edit** tab, **Detect**, **Check maps on all robots**, and the Help window (from 1.1.0).
