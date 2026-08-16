#!/usr/bin/env python3

import argparse
import tempfile
from collections import defaultdict
from contextlib import suppress
from pathlib import Path
import subprocess

from PIL import Image, ImageDraw, ImageFont, ImageOps


IMAGE_EXTENSIONS = {
    ".jpg",
    ".jpeg",
    ".png",
    ".gif",
    ".bmp",
    ".tiff",
    ".webp",
}


def find_images(root):
    """Recursively find supported image files."""
    for path in root.rglob("*"):
        if path.is_file() and path.suffix.lower() in IMAGE_EXTENSIONS:
            yield path


def make_contact_sheet(directory, paths, output_dir):
    """Create a contact sheet for all images in one directory."""

    thumb_width = 650
    thumb_height = 650
    columns = 4

    header_height = 130
    filename_height = 42

    rows = (len(paths) + columns - 1) // columns

    sheet_width = columns * thumb_width
    sheet_height = header_height + rows * (thumb_height + filename_height)

    try:
        title_font = ImageFont.truetype("DejaVuSans-Bold.ttf", 42)
        path_font = ImageFont.truetype("DejaVuSans.ttf", 22)
        file_font = ImageFont.truetype("DejaVuSans.ttf", 18)
    except OSError:
        title_font = ImageFont.load_default()
        path_font = ImageFont.load_default()
        file_font = ImageFont.load_default()

    title_ascent, title_descent = title_font.getmetrics()
    path_ascent, path_descent = path_font.getmetrics()

    mypad = 15

    header_height = 130
    filename_height = 42

    sheet = Image.new(
        "RGB",
        (sheet_width, sheet_height),
        "white",
    )

    draw = ImageDraw.Draw(sheet)

    # Header
    draw.text(
        (15, mypad),
        f"{directory.name} — {len(paths)} images",
        fill="black",
        font=title_font,
    )

    draw.text(
        (15, mypad + title_ascent + title_descent + 10),
        str(directory),
        fill="black",
        font=path_font,
    )

    # Images
    for i, path in enumerate(paths):
        with suppress(Exception):
            with Image.open(path) as im:
                im = ImageOps.exif_transpose(im).convert("RGB")
                im.thumbnail((thumb_width - 10, thumb_height - 10))

                x = (i % columns) * thumb_width
                y = header_height + (i // columns) * (thumb_height + filename_height)

                # Center image in its cell.
                px = x + (thumb_width - im.width) // 2
                py = y + (thumb_height - im.height) // 2

                sheet.paste(im, (px, py))

                # Original filename.
                draw.text(
                    (x + 5, y + thumb_height + 8),
                    f"FILENAME: {path.name}",
                    fill="black",
                    font=file_font,
                )

    output = output_dir / f"{i:03d}-{directory.name}.jpg"
    sheet.save(output, quality=90)

    return output


def main():
    parser = argparse.ArgumentParser(
        description="Create contact sheets for images grouped by directory."
    )

    parser.add_argument(
        "root",
        type=Path,
        help="Root directory to recursively search for images.",
    )

    parser.add_argument(
        "--output",
        type=Path,
        help="Output directory for contact sheets.",
    )

    parser.add_argument(
        "--open",
        action="store_true",
        help="Open generated contact sheets with qlmanage.",
    )

    args = parser.parse_args()

    root = args.root.expanduser().resolve()

    if not root.is_dir():
        parser.error(f"Not a directory: {root}")

    # Find and group images by their immediate parent directory.
    groups = defaultdict(list)

    for path in find_images(root):
        groups[path.parent].append(path)

    if not groups:
        print(f"No images found under {root}")
        return

    # Use a temporary directory unless the user supplied --output.
    if args.output:
        output_dir = args.output.expanduser().resolve()
        output_dir.mkdir(parents=True, exist_ok=True)
    else:
        output_dir = Path(tempfile.mkdtemp(prefix="contact-sheets-"))

    print(f"Root:   {root}")
    print(f"Output: {output_dir}")
    print(f"Groups: {len(groups)}")

    contact_sheets = []

    for directory, paths in sorted(groups.items()):
        paths.sort()

        print()
        print(f"directory: {directory}")
        print(f"images:    {len(paths)}")

        output = make_contact_sheet(
            directory,
            paths,
            output_dir,
        )

        contact_sheets.append(output)

    print()
    print(f"Created {len(contact_sheets)} contact sheets.")
    print(f"Output: {output_dir}")

    if args.open:
        subprocess.run(
            ["qlmanage", "-p", *map(str, contact_sheets)],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )


if __name__ == "__main__":
    main()
