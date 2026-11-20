# Start here

DRISHTI Map finds out why a laser map makes robots jump or lose their position, builds a corrected map file, and proves the correction before any robot sees it.

> This program never changes a robot. It reads from robots and writes map files into the `candidates` folder. A person loads a candidate in RoboShop, on one test robot first.

## The screen

| Part | What it is for |
|---|---|
| Left | Maps, candidates built here, recordings, robots. Click a map to show it. Shift-click a second map to lay it on top in blue. |
| Middle | The map. Wheel = zoom, drag = move. **Region** and **Box** draw an area for the build and edit tools. |
| Bottom | The text result of the last tool. **Save text** keeps it in `results`. |
| Right, **Tools** | Detect, test, measure, build, check: the steps in order. |
| Right, **Edit** | Change a map by hand. |
| Right, **Assistant** | The same tools driven by an AI model in plain language. |

## What do you want to do?

| Situation | Go to |
|---|---|
| First time: how do I use this program? | [[WI-00]] |
| Set up a workspace, robots and a recording in detail | [[WI-01]] |
| Robots jump or lose position at a place | [[WI-02]], then [[WI-03]] |
| I have a candidate and want to know if it is safe | [[WI-04]] |
| The candidate passed, how do I put it on the floor | [[WI-05]] |
| Remove a feature line, change a path direction, erase walls, move a station | [[WI-06]] |
| Are all robots on the same map? | [[WI-07]] |
| A robot stopped or was off position earlier today: what happened? | [[WI-09]] |
| Let the AI model do the steps | [[WI-08]] |
| What does this symptom mean? | [[KB-02]] |
| What do the numbers mean? | [[KB-04]] |
| How was the reference map fixed? | [[KB-03]] |
| See what the LiDAR measures, and at what height | [[KB-09]] |

## The method in six lines

1. Record a drive through the trouble spot. [[WI-01]]
2. **Detect with Recording**: where does the map make a robot jump? [[WI-02]]
3. **Find rigid pieces**: which part of the map is in the wrong place, and where does it meet the rest? [[WI-02]]
4. **Measure turn**, draw the region, **Build**: move the whole misplaced piece, not a part of it. [[WI-03]]
5. Check list all PASS, **Replay** shows no jump, **Fits as one rigid track** says about 0.1 m. [[WI-04]]
6. Trial on one robot with a recording, then the fleet. [[WI-05]]

Moving only part of a misplaced piece just moves the jump somewhere else. That is the one mistake to avoid; [[KB-03]] shows it happening.
