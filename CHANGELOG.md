# What changed in each version

Newest first. Every version listed here is under **Releases** on GitHub with its `DrishtiMap.exe`.
To publish a version: change `Version` in `app\app.go`, add a section here, commit, then push a tag
`v<version>`. GitHub builds the exe and publishes the release by itself.

## 1.3.1

- Releases are now built and published by GitHub from the source: every version tag gets its
  `DrishtiMap.exe` and zip under Releases, with this list of changes.
- Example names on the screens are neutral (`PLANTMAP_v5c` instead of a site's own map name).
- Tests read the reference maps' name from the workspace files, so no site name is written in the code.

## 1.3.0

- **What happened?** (Tools tab, Past event): pick a robot and a time that has already passed. The app
  reads the fleet manager's history, read-only, and shows where the robot drove, every stop, position
  jump, relocalisation and error, whether the connection stalled, how every other robot did in the same
  place, and the Wi-Fi events of those minutes. The trip is drawn on the map, coloured by confidence.
- **History sources** dialog: fleet manager login and optional AMR Health server, kept per workspace.
- Help: new work instruction WI-09. The assistant can use the new tool.
- A test fails the build if the script that reads the fleet manager ever gains a call that writes,
  deletes, or starts another program.

## 1.2.1

- Neutral wording in screens and help pages (no site names).

## 1.2.0

- LiDAR scan viewer with the height of each scanner's scan plane, live or from a recording.
- AI model presets and a **Test connection** button in Settings.
- Logo and program icon.
- Hand **Edit** tab, **Detect**, **Check maps on all robots**, and the Help window (from 1.1.0).
