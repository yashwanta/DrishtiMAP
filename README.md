# DRISHTI Map

Desktop tool for finding and fixing AMR laser-map faults (SEER / RoboShop `.smap`), with an optional AI assistant.
One file, no installer: **`DrishtiMap.exe`** (Windows 10/11, uses the WebView2 runtime that ships with Windows 11).

## What it is

An autonomous mobile robot (AMR) finds its position by matching what its laser scanners see against a map of the
walls. When part of that map is wrong, the robot's confidence drops, its position jumps, and it stops or
relocalises in the middle of a route. On the floor this looks like "the robot got lost at LM443 again".

The hard part is that a wrong map usually looks fine. Opening it in the map editor shows walls, stations and paths
where you expect them. The error only shows when a real robot drives through it.

DRISHTI Map answers three questions, in order:

1. **Where and why does this map make robots lose position?**
2. **What is the smallest change that corrects it?**
3. **Is the corrected file safe, before any robot sees it?**

It does this from two things you already have: the map file, and a short recording of a robot driving through the
trouble spot. It never changes a robot. Its output is a new map file and a report, which a person loads on one
test robot.

## What you can accomplish with it

**Find the real cause of a localisation problem, not just the spot where it shows.**
Replay a recorded drive through the map and the app marks every place the map would make a robot jump (a step of
0.5 m or more) and every stretch where the walls match the lasers poorly. It then finds which part of the map is
one rigid, correct piece and where that piece is attached wrongly to the rest. A jump shows at a seam; the cause is
the whole piece behind it.

**Measure the correction instead of guessing it.**
The app measures the turn and shift of the misplaced piece from the laser data, three times, and reports the value
with its spread. No trial-and-error shifting of walls.

**Build a corrected map that keeps everything consistent.**
A region moves as one unit: wall points, reflection points, reflectors, feature lines, stations, paths and bins
together. Stations stay at the same physical place on the floor because their walls move with them. Everything
outside the region stays byte-for-byte as it was.

**Prove a map before a trial.**
Any map file, including one that has never been on a robot, can be tested against a recording. A candidate also
goes through a check list: same stations, paths, bins and areas as the base; path lengths kept; no path or station
closer to a wall than before; no feature line lying on a path. You go to the floor with evidence, not hope.

**Make small map repairs by hand, safely.**
Delete a feature line that raises a virtual-laser fault, erase or move walls, take walls from another map, change
a path's direction or speed, move a station. Each save is a new checked file; the source map is never overwritten.

**See what every robot is actually running.**
One click reads the current map and its checksum from every robot and flags the ones that differ. Map names say
nothing about content: two different maps can carry the same name.

**Look at the raw LiDAR data.**
Step through a recording one scan at a time, or read a live scan from a robot, with each scanner in its own colour
and the height of its scan plane. Useful for telling a map problem from a sensor or environment problem.

**Record evidence from a robot.**
Position, confidence and laser scans every 0.7 seconds during a drive, read-only, for analysis here or for keeping
as a before/after record.

**Work in plain language.**
Connect an AI model (Claude, any OpenAI-compatible server, or a local model) and ask "why does AMR-03 jump near
LM520?". The assistant runs the same tools and explains the result. Every tool also works without a model.

**Train people on the method.**
The Help window holds 9 work instructions and 9 knowledge-base articles: how a robot uses a map, what each symptom
usually means, how to read the numbers, and a full worked case.

## Who it is for

- **Controls and AMR engineers** who have to fix a map and want to know the change is right before a trial.
- **Maintenance and floor support** who need to tell a map fault from a robot or sensor fault.
- **Site leads** who need to know whether the whole fleet is on the same map.

## What it replaced

In the case this app was built from, robots jumped about 1.4 m in one corridor. Three hand-made map patches each
passed their file checks and each failed on the floor: every one moved the jump to a different station. The laser
recordings showed why. A 73 m loop of the map was drawn correctly but attached about 2 degrees turned, which is
almost nothing at one end and 1.5 m at the other. Turning the whole loop back as one piece removed the jump on the
first trial. The app turns that method into buttons, so the next case takes hours instead of days.

## Every tool

| Step | Button (Tools tab) | What you get |
|---|---|---|
| Find what is wrong | **Detect with Recording**, **Detect (map file only)** | every finding marked on the map: jumps, low wall match, feature lines on paths, broken links |
| Are all robots on the same map | **Check maps on all robots** (left panel) | each robot's current map and checksum, the ones that differ flagged; optional download |
| Change a map by hand | **Edit** tab | delete feature lines, erase / move / take walls, set path direction or speed, move / add / delete stations and paths; saved as a checked candidate |
| See the LiDAR data | **LiDAR · what each scanner sees** (Tools tab) | one scan at a time from a recording, or live from a robot; each scanner in its own colour with the height of its scan plane |
| Learn the method | **Help** (top right) | 9 work instructions (WI) and 9 knowledge-base articles (KB), also readable by the assistant |
| Record a drive | robot → **rec** | position, confidence and lasers every 0.7 s, read-only |
| Copy the robot's map | robot → **map** | the map the robot is using now, into `maps\` |
| What the robot reported | **Recording summary** | stations, confidence, jumps, abnormal localisation states |
| Test any map with a recording | **Replay through Map** | where the map would make a robot jump (step ≥ 0.5 m) and the wall match along the route. Works on maps never loaded on a robot |
| One rigid track? | **Fits as one rigid track?** | about 0.1 m rms = the map agrees with the lasers; much more = a piece is in the wrong place |
| Find the misplaced piece | **Find rigid pieces**, **Show drift** | the stretches the map is rigid over, the step and turn at each seam |
| Measure the correction | **Measure turn** | rotation + shift, repeated three times and given with its spread |
| Build a candidate | **Build** (move region / take walls in a box / remove lines) | a new file in `candidates\` plus a check report |
| Check and compare | **Check Map against Second map**, **Compare** | every difference, and every safety check (including a feature line lying on a path) |

The **Assistant** tab is the same tools driven by an AI model in plain language.

## Rules built into the program

- **Read-only on robots.** The only requests it can send are: robot information (1000), position (1004), laser scan (1009), map list (1300) and map download (4011). There is no code that loads a map, changes a parameter, relocalises or moves a robot. A test (`seer/seer_test.go`) fails if a write request is ever added. The list is shown in Settings.
- **Candidates are files, not deployments.** A candidate is written to `candidates\` and never replaces a source map or an existing candidate. A person loads it in RoboShop, on one test robot first.
- **The AI model sees summaries only.** It receives the text result of each tool (what you see in the Output panel), never the map or laser files. With a local model (Ollama, LM Studio) nothing leaves the PC.
- **API keys** are stored encrypted with Windows DPAPI for the current Windows user (`%APPDATA%\DRISHTI Map\config.json`).
- The window talks to a server on `127.0.0.1` only, protected by a per-start token.

## Connecting an AI model (Settings)

| Type | Address | Model |
|---|---|---|
| Claude (Anthropic API) | `https://api.anthropic.com` | `claude-opus-5-5` (default) or any Claude model id |
| OpenAI-compatible | `https://api.openai.com/v1`, a Gemini/Mistral/OpenRouter/Azure v1 endpoint, LM Studio … | the model name on that server |
| Ollama on this PC | `http://127.0.0.1:11434/v1` | e.g. a tool-capable local model you have pulled |

The model must support tool (function) calling. Small local models can run the tools but are weaker at working out *why* a map is wrong; the tools themselves give the same numbers whichever model is connected.
On Claude Opus 5.5 / Fable the app asks the API for server-side refusal fallbacks, so a declined request is re-served by another Claude model instead of just stopping.

## Workspace

A workspace is one folder (one site):

```
workspace\
  maps\         source maps (.smap): imported, or downloaded from a robot
  candidates\   files built by this tool + NAME.report.txt
  drives\       recordings (loc_NNNN.json, laser_NNNN.json, locb_NNNN.json)
  results\      saved text and pictures
  robots.json   name + IP of robots (read-only access)
```

Click the workspace path at the top to switch folder. `DrishtiMap.exe` uses a `workspace` folder next to itself if there is one.

## The method (what the reference case taught)

1. Replay a recording through the map. The live map of the reference plant steps 1.36 m at LM520: the jump seen on the floor.
2. *Find rigid pieces* shows the east loop is one rigid piece (73 m, 0.09 m rms) turned about 2° against the west.
3. *Measure turn* gives the correction (2.07° ± 0.17° here; the v5 that ran on AMR-03 used 2.19°).
4. *Build → move a region* moves the whole piece with its stations, paths, lines and bins; the check list must be all PASS.
5. Replay the same recording through the candidate: no jump, and it fits as one rigid track (0.09 m rms).
6. Trial on one robot with a recording. A passed replay is evidence, not proof: it says nothing about places the recording did not drive.

Moving only part of a misplaced piece just moves the jump (that is what the earlier v3 / v4 files did).

## Limits to know

- The replay answers "does this map agree with these lasers". It does not simulate the robot's own localiser; low wall match (< 40 %) is a warning of low confidence, not a prediction of the exact value.
- The measured turn has a spread (reported). At 40 m from the pivot, ±0.17° is about ±0.12 m.
- Where the robot was relocalised during a recording, the laser track restarts; the step there is marked and not counted as a map fault.
- Checks cover geometry and properties. They do not replace a physical check of docking positions.
- **Fits as one rigid track** is only fair on a drive without long stops, up to about 120 m. On a long stop-and-go drive the laser track itself drifts; Detect then reports a warning, not a fault, when the replay shows no step.
- A misplaced piece of map cannot be seen in the map file alone; it needs a recording. A map-only double-wall detector was tried and removed: it could not tell the faulty live map from the fixed one.
- Adding a feature line by hand is not offered (the side flag of a line could not be derived safely from the files). Take lines from another map, or draw them in RoboShop.
- Stations and paths added in the Edit tab exist only in the map file; the fleet scene needs them too. Added objects have not been loaded in RoboShop yet.
- Tested with SEER maps from one site. Other map generations may carry fields this version has not seen; unknown fields are always written back unchanged.

## Build and test

```
build.cmd                      builds DrishtiMap.exe (needs Go)
go test ./...                  unit tests; the reference-case regression tests run when these are set:
  set DRISHTI_WS=<workspace>   (maps\ and drives\ of the reference case)
  set DRISHTI_REF=<folder with moved.smap, final.smap, T.json from the original scripts>
  set DRISHTI_OUT=<folder with d6_poses.txt from the original analysis>
DrishtiMap.exe --no-window --port 47811      server only; prints the address
node tests\e2e.js <address>                  every tool end to end on the workspace
node tests\e2e_edit.js <address>             detect, hand edits, help and fleet check end to end
node tests\ui.js <address> shot.png "map=NAME&drive=NAME&run=replay"   opens the page in headless Edge
```

Source layout: `smap` (map file reader/writer that keeps every field), `mapedit` (build, check, compare), `lasertrack` (laser track, replay, fits), `seer` (read-only robot client), `app` (workspace, tools), `help` (work instructions and knowledge base, Markdown in `help\docs`), `ai` (model connections), `server` (window back end + `web` front end).
