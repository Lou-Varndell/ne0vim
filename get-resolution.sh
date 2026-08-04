#!/usr/bin/env bash


# get width height
fd -a --type f --changed-within 5h -0 |
while IFS= read -r -d '' f; do
    read w h < <(
        sips -g pixelWidth -g pixelHeight "$f" 2>/dev/null |
        awk '/pixelWidth/{w=$2} /pixelHeight/{h=$2} END{print w, h}'
    )
    printf "%5s %5s %s\n" "$w" "$h" "$f"
done | sort -k1,1n -k2,2n

species='woodpecker'
photoshoot='woodpecker-red-tailed'
# find photo shoot
fd -t f -i $species

# Preview once found
fd -t f -i 'woodpecker-red-tailed' -0 |
python3 -c '
import os, sys, subprocess

paths = [p.decode() for p in sys.stdin.buffer.read().split(b"\0") if p]
paths.sort(key=lambda p: os.path.basename(p).lower())

subprocess.run(["open", "-a", "Preview", *paths])
'

# # double check photoshoot for dups
fd -t f -i 'woodpecker-red-tailed' -0 |
python3 -c '
import os, sys
paths = [p.decode() for p in sys.stdin.buffer.read().split(b"\0") if p]
for p in sorted(paths, key=lambda p: os.path.basename(p).lower()):
    print(p)
'

mkdir -p 'woodpecker/red-tailed_barn'

fd -t f -i 'woodpecker-red-tailed' -0 |
while IFS= read -r -d '' f; do
    printf 'mv -n -- %q woodpecker/red-tailed_barn\n' "$f"
done

fd -t f -i 'woodpecker-red-tailed' -0 |
while IFS= read -r -d '' f; do
    mv -n -- "$f" woodpecker/red-tailed_barn/
done

fd -a --type f --changed-within 5h -0 |
uvpy -c '
import sys
from PIL import Image

for name in sys.stdin.buffer.read().split(b"\0"):
    if not name:
        continue
    path = name.decode()
    try:
        with Image.open(path) as im:
            print(f"{im.width}x{im.height}\t{path}")
    except Exception:
        pass
'