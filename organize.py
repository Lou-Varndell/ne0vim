#!/usr/bin/env python3

from pathlib import Path
import shutil

ROOT = Path.home() / "Images"
DEST = ROOT / "woodpecker"
PATTERN = "red-tailed-woodpecker"

DEST.mkdir(exist_ok=True)

matches = sorted(
    (p for p in ROOT.rglob("*") if p.is_file() and PATTERN.lower() in p.name.lower()),
    key=lambda p: p.name.lower(),
)

for src in matches:
    dest = DEST / src.name

    if dest.exists():
        stem = dest.stem
        suffix = dest.suffix
        n = 2
        while True:
            candidate = DEST / f"{stem}-{n}{suffix}"
            if not candidate.exists():
                dest = candidate
                break
            n += 1

    print(f"{src.relative_to(ROOT)}")
    print(f"  -> {dest.name}")

    # Uncomment when you're happy with the output
    # shutil.move(src, dest)

from PIL import Image

matches.sort(
    key=lambda p: (
        Image.open(p).size,
        p.name.lower(),
    )
)


def key(p):
    with Image.open(p) as im:
        return (p.name.lower(), im.width * im.height)


matches.sort(key=key)

SPECIES = [
    "red-tailed-woodpecker",
    "pileated-woodpecker",
    "downy-woodpecker",
]

import hashlib


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        while chunk := f.read(1024 * 1024):
            h.update(chunk)
    return h.hexdigest()


from PIL import Image

with Image.open(path) as im:
    print(im.width, im.height)

PATTERN = "red-tailed-woodpecker"
DEST = ROOT / PATTERN

PATTERN = "barn-swallow"

# python organize.py "red-tailed-woodpecker"
# python organize.py "pileated-woodpecker"
# python organize.py "blue-jay"
