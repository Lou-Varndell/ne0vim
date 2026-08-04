#!/usr/bin/env python3


import sys
from pathlib import Path
from PIL import Image, ImageTk
import tkinter as tk

ROOT = Path.home() / "Images"

if len(sys.argv) < 2:
    print("Usage: birds.py <search-pattern>")
    sys.exit(1)

pattern = sys.argv[1].lower()

files = sorted(
    [p for p in ROOT.rglob("*") if p.is_file() and pattern in p.name.lower()],
    key=lambda p: p.name.lower(),
)

if not files:
    print("No matches.")
    sys.exit(0)

idx = 0

root = tk.Tk()
root.title("Bird Browser")

img_label = tk.Label(root)
img_label.pack()

info = tk.Label(root, font=("Helvetica", 12))
info.pack()


def show():
    global photo
    p = files[idx]
    im = Image.open(p)
    w, h = im.size
    im.thumbnail((1400, 900))
    photo = ImageTk.PhotoImage(im)
    img_label.configure(image=photo)
    info.configure(text=f"{idx + 1}/{len(files)}   {w}x{h}\n{p}")


def next_img(event=None):
    global idx
    if idx < len(files) - 1:
        idx += 1
        show()


def prev_img(event=None):
    global idx
    if idx > 0:
        idx -= 1
        show()


root.bind("<Right>", next_img)
root.bind("<Left>", prev_img)
root.bind("q", lambda e: root.destroy())
root.bind("<Escape>", lambda e: root.destroy())

show()
root.mainloop()
