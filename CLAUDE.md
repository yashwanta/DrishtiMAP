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
   robot (AMR-03 at Springfield) first. Claude never loads a map on a robot.
3. **Claude does read-only checks only** on plant servers and robots. Anything that changes a robot or
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
| `app` | workspace, job runner, the tools (`tools.go`, `tools2.go`, `scan.go`), key storage |
| `ai` | Claude (official Go SDK v2) and OpenAI-compatible connections, system prompt, connection test |
| `help` | help articles (Markdown, embedded) |
| `server` | local server on 127.0.0.1 + `web\` front end (`app.js`, `edit.js`, `scan.js`) |
| `build\mklogo` | cuts icon and header logo out of `NOC-LOGO4.png` |
| `tests` | `e2e.js`, `e2e_edit.js`, `ui.js` (headless Edge), `reference\` (original v5 build outputs) |
| `workspace` | Springfield maps, recordings, candidates, `robots.json` (12 robots) |

## Build and test

```
build.cmd                                   vet + build DrishtiMap.exe (icon comes from rsrc_windows_amd64.syso)
go run ./build/mklogo                       regenerate build\icon.ico and server\web\logo.png from NOC-LOGO4.png

set DRISHTI_WS=D:\DRISHTI\Drishti map\workspace
set DRISHTI_REF=D:\DRISHTI\Drishti map\tests\reference
set DRISHTI_OUT=D:\DRISHTI\Drishti map\tests\reference
go test ./...                               unit + Springfield regression tests

set DRISHTI_MAPDIRS=<folders with .smap files, separated by ;>
go test ./mapedit -run TestAllRealMaps -v   every real map: read, byte-identical write, checks, detector

DrishtiMap.exe --no-window --port 47821     server only; prints the address with its token
node tests\e2e.js <address>                 every analysis/build tool end to end (several minutes)
node tests\e2e_edit.js <address>            detect, hand edits, help, fleet check
node tests\ui.js <address> shot.png "map=NAME&drive=NAME&run=replay"    page test in headless Edge
```

Release: bump `Version` in `app\app.go`, build, copy the exe and `READ ME FIRST.txt` to
`release\DRISHTI-Map-<version>\`, zip it.

## Things that bite

- **Backslashes get halved** in Git Bash heredocs on this PC (`\n` becomes a real newline, `\\` becomes
  `\`). Use the Edit/Write tools for any Go or JS text that contains a backslash.
- **`go run github.com/akavel/rsrc ...` is blocked for Claude** by the permission system. The user ran it
  once; the resulting `rsrc_windows_amd64.syso` stays in the folder and is linked by every build.
  If the icon changes, the user reruns:
  `go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico build/icon.ico -o rsrc_windows_amd64.syso`
- **Never `taskkill /IM DrishtiMap.exe`**: it also closes the user's open app. Build test copies under
  another name (`DrishtiMap_new.exe`) and kill only that name.
- `--workspace` is for one run only and does not change the saved workspace.
- The test `tests\e2e.js robot` step replaces the robot list for a moment and restores it.
- Plain `msedge --headless --screenshot` hangs here; `tests\ui.js` drives Edge over the DevTools port.

## What was learned about map analysis (do not relearn)

- A jump shows at a seam; the cause is the whole misplaced piece behind it. Move the whole piece once
  (Springfield: the east loop, one rigid 73 m piece turned about 2.1 degrees). Partial moves only move
  the jump (v3, v4).
- The per-scan "tracking replay" is unstable (a 1.3 cm grid shift flips the verdict). Use the stretch
  replay (8 m stretches, step >= 0.5 m = jump). Do not quote per-scan pull tables as proof.
- Wall dots sit on a 5 cm lattice: grid cells must be centred on it.
- The measured turn has a spread (Springfield 2.07 +- 0.17 degrees; v5 used 2.19). Report value +- spread.
- "Fits as one rigid track" is only fair up to about 120 m without stops. A long hand-driven drive drifts
  (the 140 m trial gives 0.8 m rms on a map with zero replay steps): Detect reports that as a warning.
- A misplaced piece cannot be seen in the map file alone. A map-only double-wall detector was tried and
  removed: it could not tell the live map from the fixed one.
- A feature line within about 0.3 m of a path centre raises a virtual-laser fault (LM514-LM515, 0.12 m).
- The side flag of a feature line (direction +-1 vs directionPos) could not be derived from the files,
  so adding feature lines by hand is not offered.
- A map name says nothing about content: compare md5. Always build from the file on the robot today.
- Springfield robots: front scanner FocueRay h100 at 0.194 m, rear c200 at 0.197 m above the floor.

## State (2026-11-17, version 1.2.0)

- App complete: detect, replay, pieces, measure turn, build (move region / merge box / remove lines),
  hand Edit tab, checks, compare, fleet map check, LiDAR scan viewer with scanner heights, recorder,
  Help (WI/KB), AI assistant with provider presets and Test connection, logo and exe icon.
- All unit tests, both end-to-end suites and the real-map sweep (35 files, including 14 Hopkinsville
  maps from a September debug dump in `D:\DRISHTI\Troubleshoot-Fleetmanager\...\maps`) pass.
- Standalone run verified as a brand-new user (exe alone in an empty folder).

### Springfield floor state (last read 2026-11-17 20:26, read-only)

- 11 of 12 robots answered (AMR-11 not reachable); all run map name `SPRAMRMAP`, md5 `469f9529`.
  That is the ORIGINAL v5 with only the name changed: it still has feature line #2642
  (36.16,35.26)-(36.32,35.25), 0.12 m from path LM514-LM515, and it lacks the user's edit
  (LM517..LM521 direction forward). Who rolled it out is not known.
- `SPRAMRMAP_v5c` = the user's edited copy minus that line; all checks pass, replay shows no jump.
  It is in `workspace\candidates\` and `D:\DRISHTI\DRISHTI-Feed\MAP-Springfield\v5c\`. Not on any robot.
- Robot addresses are in `workspace\robots.json`.

## Open items

Floor (user):
1. Decide on the fleet map; load `SPRAMRMAP_v5c` on AMR-03 in RoboShop, trial run with a recording,
   then Claude verifies the recording.
2. Reverse-direction drive and dock checks at AP523, AP524, PP560.
3. Find out who rolled v5 out to the fleet.

App (needs the user):
1. Connect a real AI model (company gateway: "Other OpenAI-compatible server") and run a real
   conversation test; then the mock provider test can be replaced.
2. Open the real window once and report anything that looks wrong (pages were only tested headless).
3. Record a drive from inside the app (recorder not yet run against a robot from the app).
4. Open a map with added stations/paths in RoboShop.
5. The exe is unsigned: IT must sign it to remove the SmartScreen warning.
6. Not tested: start on a PC without the WebView2 runtime; a second PC.

Other plants:
- Hopkinsville: needs one current map and one short recording (replay and scan viewer untested there).
- ICON, Jonesville: confirm they run SEER robots with `.smap` maps.
