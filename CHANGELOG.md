# What changed in each version

Newest first. Every version stays under **Releases** on GitHub with its `DrishtiMap.exe`; old versions
are never removed, so anyone can go back to an earlier one. Each heading says when the version was
released and who released it.

To publish a version: change `Version` in `app\app.go`, add a section at the top of this list as
`## <version> · <date> · by <your GitHub name>`, commit, then push a tag `v<version>`. GitHub builds the
exe and publishes the release by itself, with your name and the list of commits on the release page.

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
