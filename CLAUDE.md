# DRISHTI Map — project memory

Claude Code reads this file automatically when it is started in this folder. It holds what a new
session needs to carry on. Keep it current: update the "State" and "Open items" sections at the end
of a working session.

## What this is

DRISHTI Map is a Windows desktop app (one exe, Go + WebView2 window, HTML/JS screens) that finds out
why a SEER / RoboShop AMR laser map (`.smap`) makes robots jump or lose position, builds a corrected
map file, and checks it before any robot sees it. An AI model is optional; every tool works without one.

- User guide: `README.md`. In-app help: `help\docs\` (WI-01..08 work instructions, KB-01..09 knowledge base).
- Shipping package: `release\DRISHTI-Map-<version>.zip` (exe + READ ME FIRST.txt).

## Rules that must not be broken

1. **Read-only on robots.** `seer\seer.go` may only send requests 1000, 1004, 1009, 1300 (port 19204)
   and 4011 (port 19207). `seer\seer_test.go` fails if the list grows. Never add a request that loads
   a map, changes a parameter, relocalises or moves a robot.
2. **Candidates are files.** Builds and edits write new files to `workspace\candidates\`; they never
   overwrite a source map or an existing candidate. A person loads a candidate in RoboShop, on ONE test
   robot first. Claude never loads a map on a robot.
3. **Read-only on plant servers.** `fleet\history.py` is the only thing the app runs on a fleet manager; it may only open files for reading and print. `fleet\fleet_test.go` fails if it gains a write, delete, network or process call, or if a setting could carry a command. **Claude does read-only checks only** on plant servers and robots. Anything that changes a robot or
   server is a script or file the user runs.
4. **No mock data in the app.** Tests run on the real maps and recordings in `workspace\`. The only mock
   left is the AI provider test (`ai\chat_test.go`), until a real API key or gateway is available.
5. **The AI model receives tool result text only**, never map or laser files. Keys are DPAPI-encrypted
   per Windows user in `%APPDATA%\DRISHTI Map\config.json`.
6. Treat site data (robot addresses, map content, names) as private: never send it to outside services.

## Layout

| Folder | Content |
|---|---|
| `smap` | `.smap` reader/writer that keeps every field and key order (round trip is byte-identical) |
| `mapedit` | move region, merge box, hand edits (`edit.go`), checks (`check.go`), compare, detector (`detect.go`) |
| `lasertrack` | laser track, stretch replay, rigid fit, pieces, measure turn, per-scanner scans (`scan.go`) |
| `seer` | read-only robot client |
| guided workflow | `server\web\guided.js` (eight steps, cases), `fleet\area.go` (area verdict), `app\area.go` + `app\evidence.go` (tools `area_report`, `box_evidence`, `trial_result`, cases). Page tests: `tests\guided_flow.js` (box fix must stop at Build), `tests\guided_piece.js` (the reference piece move: turn about 2 deg, seam repair, 0 jumps). `app\arm.go`: armed recording, evidence sources. `fleet\scans.go` + `app\scans.go`: laser scans stored by AMR Health (sign-in kept DPAPI-encrypted in the user config, never in the workspace); stand-in server for tests `tests\health_standin.js` (serves real recorded replies), page test `tests\guided_scans.js`. Never round-trip `%APPDATA%\DRISHTI Map\config.json` through PowerShell `Set-Content`: it adds a BOM the app cannot read |
| `fleet` | past events: fleet manager history over the user's ssh login (`history.py` runs there, read-only), trip analysis, comparison with other robots, Wi-Fi history from AMR Health |
| `app` | workspace, job runner, the tools (`tools.go`, `tools2.go`, `scan.go`), key storage |
| `ai` | Claude (official Go SDK v2) and OpenAI-compatible connections, system prompt, connection test |
| `help` | help articles (Markdown, embedded) |
| `server` | local server on 127.0.0.1 + `web\` front end (`app.js`, `edit.js`, `scan.js`) |
| `build\mklogo` | cuts icon and header logo out of `NOC-LOGO4.png` |
| `tests` | `e2e.js`, `e2e_edit.js`, `ui.js` (headless Edge), `reference\` (original v5 build outputs) |
| `workspace` | site maps, recordings, candidates, `robots.json` (not in git) |

## Build and test

```
build.cmd                                   vet + build DrishtiMap.exe (icon comes from rsrc_windows_amd64.syso)
go run ./build/mklogo                       regenerate build\icon.ico and server\web\logo.png from NOC-LOGO4.png

set DRISHTI_WS=<this folder>\workspace
set DRISHTI_REF=<this folder>\tests\reference
set DRISHTI_OUT=<this folder>\tests\reference
go test ./...                               unit + reference-case regression tests

set DRISHTI_MAPDIRS=<folders with .smap files, separated by ;>
go test ./mapedit -run TestAllRealMaps -v   every real map: read, byte-identical write, checks, detector

DrishtiMap.exe --no-window --port 47821     server only; prints the address with its token
node tests\e2e.js <address>                 every analysis/build tool end to end (several minutes)
node tests\e2e_edit.js <address>            detect, hand edits, help, fleet check
node tests\ui.js <address> shot.png "map=NAME&drive=NAME&run=replay"    page test in headless Edge
```

Release (GitHub builds and publishes it, `.github\workflows\release.yml`): bump `Version` in `app\app.go`,
add a `## <version>` section to `CHANGELOG.md` saying what changed in plain words, commit, push, then
`git tag v<version>` and `git push origin v<version>`. The workflow refuses a tag that does not match the
version or has no changelog section. The exe and zip appear under Releases a few minutes later.
`packaging\READ ME FIRST.txt` goes into the zip. Do this for every change the team should get.

## Things that bite

- **Backslashes get halved** in Git Bash heredocs on this PC (`\n` becomes a real newline, `\\` becomes
  `\`). Use the Edit/Write tools for any Go or JS text that contains a backslash.
- **`go run github.com/akavel/rsrc ...` is blocked for Claude** by the permission system. The user ran it
  once; the resulting `rsrc_windows_amd64.syso` stays in the folder and is linked by every build.
  If the icon changes, the user reruns:
  `go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico build/icon.ico -o rsrc_windows_amd64.syso`
- **Never `taskkill /IM DrishtiMap.exe`**: it also closes the user's open app. Build test copies under
  another name (`DrishtiMap_new.exe`) and kill only that name.
- **Fleet history lessons:** a robot turning on the spot is normal (only flag a turn when confidence collapses); unchanged data then a leap of metres is a connection stall, not a position jump; a parked robot looks the same as no data; robot clocks are wrong, use the fleet manager line time. Real-event regression: `workspace\history\trip_amr08_20261120.tsv` (`go test ./fleet` with DRISHTI_WS).
- `--workspace` is for one run only and does not change the saved workspace.
- The test `tests\e2e.js robot` step replaces the robot list for a moment and restores it.
- Plain `msedge --headless --screenshot` hangs here; `tests\ui.js` drives Edge over the DevTools port.

## What was learned about map analysis (do not relearn)

- A jump shows at a seam; the cause is the whole misplaced piece behind it. Move the whole piece once
  (reference case: the east loop, one rigid 73 m piece turned about 2.1 degrees). Partial moves only move
  the jump (v3, v4).
- The per-scan "tracking replay" is unstable (a 1.3 cm grid shift flips the verdict). Use the stretch
  replay (8 m stretches, step >= 0.5 m = jump). Do not quote per-scan pull tables as proof.
- Wall dots sit on a 5 cm lattice: grid cells must be centred on it.
- The measured turn has a spread (reference case 2.07 +- 0.17 degrees; v5 used 2.19). Report value +- spread.
- "Fits as one rigid track" is only fair up to about 120 m without stops. A long hand-driven drive drifts
  (the 140 m trial gives 0.8 m rms on a map with zero replay steps): Detect reports that as a warning.
- A misplaced piece cannot be seen in the map file alone. A map-only double-wall detector was tried and
  removed: it could not tell the live map from the fixed one.
- A feature line within about 0.3 m of a path centre raises a virtual-laser fault (LM514-LM515, 0.12 m).
- The side flag of a feature line (direction +-1 vs directionPos) could not be derived from the files,
  so adding feature lines by hand is not offered.
- A map name says nothing about content: compare md5. Always build from the file on the robot today.
- Reference plant robots: front scanner FocueRay h100 at 0.194 m, rear c200 at 0.197 m above the floor.

## GitHub (public, source for contributors)

github.com/yashwanta/DrishtiMAP is public and holds the source code so contributors can work on it
(user decision 2026-11-20; on 2026-11-19 the source had been taken off for a day).

- Local branch `main` tracks `origin/main`. Site data never goes there: `workspace\` (maps, recordings,
  robot addresses, `site.json` with the server login), `.smap` files, map screenshots, exes and `release\`
  are in `.gitignore`. Contributors get test data from the owner, outside GitHub.
- Never commit the company name, plant names, robot addresses, maps, recordings or screenshots of maps. Say
  "the reference plant" or "the reference case". Floor notes and open items are in `CLAUDE.local.md` (not in git).
