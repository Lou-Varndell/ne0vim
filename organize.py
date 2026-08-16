#!/usr/bin/env python3

import argparse
import hashlib
import shutil
import subprocess
from datetime import datetime
from pathlib import Path

from PIL import Image


ROOT = Path.home() / "Images"


def find_matches(root: Path, pattern: str) -> list[Path]:
    pattern = pattern.lower()
    return [p for p in root.rglob("*") if p.is_file() and pattern in str(p).lower()]


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        while chunk := f.read(1024 * 1024):
            h.update(chunk)
    return h.hexdigest()


def resolution(path: Path) -> str:
    try:
        with Image.open(path) as im:
            return f"{im.width}x{im.height}"
    except Exception:
        return "?x?"


def resolve_destination(src: Path, dest_dir: Path) -> Path | None:
    """Return the path to move src to, or None if it's a duplicate of an existing file."""
    dest = dest_dir / src.name
    if not dest.exists():
        return dest

    if sha256(src) == sha256(dest):
        return None

    stem, suffix = dest.stem, dest.suffix
    n = 2
    while True:
        candidate = dest_dir / f"{stem}-{n}{suffix}"
        if not candidate.exists():
            return candidate
        n += 1


def main():
    parser = argparse.ArgumentParser(
        description="Find and organize images by filename pattern."
    )
    parser.add_argument(
        "pattern",
        nargs="?",
        default="",
        help="substring to match against filenames (default: all files)",
    )
    parser.add_argument(
        "--root", type=Path, default=ROOT, help=f"search root (default: {ROOT})"
    )
    parser.add_argument(
        "--dest", type=Path, help="destination folder (default: <root>/<pattern>)"
    )
    parser.add_argument(
        "--preview",
        action="store_true",
        help="open matches in Preview.app before moving",
    )
    parser.add_argument(
        "--execute",
        action="store_true",
        help="actually move files (default is dry-run)",
    )
    args = parser.parse_args()
    default_name = args.pattern or datetime.now().strftime("%Y%m%d%H%M%S")
    dest_dir = args.dest or (args.root / default_name)

    matches = find_matches(args.root, args.pattern)
    if not matches:
        print(f"No matches for '{args.pattern}' under {args.root}")
        return

    if args.preview:
        subprocess.run(["open", "-a", "Preview", *(str(p) for p in matches)])

    if args.execute:
        dest_dir.mkdir(parents=True, exist_ok=True)

    for src in matches:
        dest = resolve_destination(src, dest_dir)
        if dest is None:
            print(f"  skip (duplicate): {src.relative_to(args.root)}")
            continue

        print(
            f"  {src.relative_to(args.root)} -> {dest.relative_to(args.root) if dest.is_relative_to(args.root) else dest}"
        )
        if args.execute:
            shutil.move(src, dest)

    if not args.execute:
        print("\nDry run only. Re-run with --execute to move files.")


if __name__ == "__main__":
    main()
