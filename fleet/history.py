# DRISHTI Map - reads the fleet manager's robot status history. READ-ONLY.
# It is sent to the fleet manager over SSH and run as:  python3 - <mode> <logdir> ...
# It opens the history files for reading and prints text. It changes nothing on the server.
#
#   trip <logdir> <robot> <from> <to>            one row per second for one robot
#   lane <logdir> <x0> <y0> <x1> <y1> <from> <to> <step>   every robot's passes through a box
# <from>/<to> are file-name stamps of the form 2026-11-20_06-20 (fleet manager time).
import sys, os, glob, json


def log_files(logdir, a, b):
    out = []
    for f in sorted(glob.glob(os.path.join(logdir, '*.log'))):
        n = os.path.basename(f)[:16]
        if a <= n <= b:
            out.append(f)
    return out


def lines(fs):
    total = len(fs)
    for i, f in enumerate(fs):
        if i % 5 == 0:
            print('P\t%d\t%d' % (i, total))
            sys.stdout.flush()
        try:
            fh = open(f, 'r', errors='ignore')
        except OSError:
            continue
        with fh:
            for line in fh:
                j = line.find('{')
                if j < 0:
                    continue
                try:
                    yield line[:19], json.loads(line[j:])
                except ValueError:
                    continue


def clean(s):
    return str(s).replace('\t', ' ').replace('\r', ' ').replace('\n', ' ').strip()


def alarms(k):
    out = []
    for lvl in ('fatals', 'errors', 'warnings'):
        for e in k.get(lvl) or []:
            if isinstance(e, dict) and e.get('code') is not None:
                out.append((lvl, e.get('code'), e.get('desc') or e.get('describe') or ''))
    return out


def trip(logdir, robot, a, b):
    fs = log_files(logdir, a, b)
    print('H\t%d' % len(fs))
    seen, prev = set(), set()
    for t, d in lines(fs):
        rep = d.get('report') or []
        for r in rep:
            seen.add(r.get('vehicle_id'))
        r = [x for x in rep if x.get('vehicle_id') == robot]
        if not r:
            print('M\t%s' % t)
            continue
        r = r[0]
        k = r.get('rbk_report') or {}
        if k.get('x') is None:
            print('M\t%s' % t)
            continue
        al = alarms(k)
        now = set(c for _, c, _ in al)
        for lvl, c, desc in al:
            if c not in prev:
                print('E\t%s\t%s\t%s\t%s' % (t, lvl, c, clean(desc)[:300]))
        prev = now
        path = ';'.join('%s>%s' % (p.get('source_id'), p.get('end_id'))
                        for ar in r.get('area_resources_occupied') or [] for p in ar.get('path_occupied') or [])
        print('R\t%s\t%.3f\t%.3f\t%.1f\t%.3f\t%s\t%s\t%s\t%s\t%s\t%s' % (
            t, k.get('x'), k.get('y'), (k.get('angle') or 0) * 57.29578, k.get('confidence') or 0,
            clean(k.get('current_station') or ''), k.get('reloc_status'), clean(k.get('current_map') or ''),
            r.get('connection_status'), clean(path), ','.join(str(c) for c in sorted(now, key=str))))
    for v in sorted(x for x in seen if x):
        print('V\t%s' % clean(v))


def lane(logdir, x0, y0, x1, y1, a, b, step):
    fs = log_files(logdir, a, b)
    print('H\t%d' % len(fs))
    # when a map was pushed to the fleet: the scene files beside the history carry the time in their name
    for f in sorted(glob.glob(os.path.join(os.path.dirname(logdir.rstrip('/')), 'scenes', '*.scene'))):
        print('S\t%s' % os.path.basename(f)[:19])
    passes = {}
    seenc = set()
    allr = {}  # robot -> [moving samples, sum of confidence, last x, last y] over the whole plant

    def close(v):
        p = passes.pop(v, None)
        if not p or p['n'] < 2:
            return
        print('L\t%s\t%s\t%s\t%d\t%.2f\t%.2f\t%d\t%d\t%s\t%s\t%.2f\t%.2f\t%.2f\t%.2f' % (
            p['t0'], v, p['t1'], p['n'] * step, min(p['c']), sum(p['c']) / len(p['c']),
            p['still'] * step, p['reloc'], p['map'], ','.join(str(c) for c in sorted(p['codes'], key=str)),
            p['lx'] - p['x0'], p['ly'] - p['y0'], p['mx'], p['my']))

    i = 0
    for t, d in lines(fs):
        i += 1
        if i % step:
            continue
        for r in d.get('report') or []:
            v = r.get('vehicle_id')
            k = r.get('rbk_report') or {}
            x, y = k.get('x'), k.get('y')
            if x is None or y is None:
                continue
            c = k.get('confidence') or 0
            g = allr.get(v)
            if g is None:
                g = allr[v] = [0, 0.0, x, y]
            elif abs(x - g[2]) + abs(y - g[3]) > 0.05:
                g[0] += 1
                g[1] += c
            g[2], g[3] = x, y
            if not (x0 <= x <= x1 and y0 <= y <= y1):
                if v in passes:
                    passes[v]['miss'] += 1
                    if passes[v]['miss'] * step > 30:
                        close(v)
                continue
            p = passes.get(v)
            if p is None:
                p = passes[v] = {'t0': t, 'n': 0, 'c': [], 'still': 0, 'reloc': 0, 'map': clean(k.get('current_map') or ''),
                                 'codes': set(), 'miss': 0, 'lx': x, 'ly': y, 'x0': x, 'y0': y, 'mx': x, 'my': y, 'mc': 9, 'px': 1e9, 'py': 1e9}
            elif abs(x - p['lx']) < 0.02 and abs(y - p['ly']) < 0.02:
                p['still'] += 1
            p['miss'] = 0
            p['t1'] = t
            p['n'] += 1
            p['c'].append(c)
            p['lx'], p['ly'] = x, y
            if c < p['mc']:
                p['mc'], p['mx'], p['my'] = c, x, y
            if abs(x - p['px']) + abs(y - p['py']) > 0.3:   # a point to draw
                p['px'], p['py'] = x, y
                print('A\t%s\t%.2f\t%.2f\t%.2f' % (v, x, y, c))
            if k.get('reloc_status') not in (1, None):
                p['reloc'] += 1
            for lvl in ('fatals', 'errors'):
                for e in k.get(lvl) or []:
                    if isinstance(e, dict) and e.get('code') is not None:
                        p['codes'].add(e.get('code'))
                        if e.get('code') not in seenc:
                            seenc.add(e.get('code'))
                            print('C\t%s\t%s' % (e.get('code'), clean(e.get('desc') or '')[:240]))
    for v in list(passes):
        close(v)
    for v in sorted(x for x in allr if x):
        print('G\t%s\t%d\t%.1f' % (v, allr[v][0], allr[v][1]))


if __name__ == '__main__':
    m = sys.argv[1]
    if m == 'trip':
        trip(sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5])
    elif m == 'lane':
        lane(sys.argv[2], float(sys.argv[3]), float(sys.argv[4]), float(sys.argv[5]), float(sys.argv[6]),
             sys.argv[7], sys.argv[8], max(1, int(sys.argv[9])))
    print('END')
