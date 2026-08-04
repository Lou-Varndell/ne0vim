from pathlib import Path
from PIL import Image

for p in Path.home().rglob("*.jpg"):
    try:
        with Image.open(p) as im:
            print(f"{im.width}x{im.height}\t{p}")
    except Exception:
        pass
