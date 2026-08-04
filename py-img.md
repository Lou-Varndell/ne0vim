I would rewrite it as a proper Qt application rather than trying to adapt the Tk version. The resulting code will actually be simpler because Qt already provides image widgets, keyboard handling, scrolling, and layouts.

Step 1: Create a new project
mkdir bird-browser
cd bird-browser

uv init
uv add pyside6 pillow

You don't actually need Pillow for the first version since Qt can load JPEGs directly, but we'll probably want it later for EXIF and image processing.

Step 2: Replace Tkinter

Instead of

import tkinter as tk
from PIL import Image, ImageTk

you'll use

from PySide6.QtWidgets import (
    QApplication,
    QLabel,
    QMainWindow,
    QWidget,
    QVBoxLayout,
)
from PySide6.QtGui import QPixmap
from PySide6.QtCore import Qt
Step 3: Think in terms of widgets

Instead of

Tk
    Label(image)
    Label(text)

Qt is more like

QMainWindow
    QWidget
        QVBoxLayout
            QLabel(image)
            QLabel(status)
Step 4: Displaying an image

Instead of

Image.open(...)
ImageTk.PhotoImage(...)

you simply do

pixmap = QPixmap(str(path))
label.setPixmap(pixmap)

Qt reads JPEG, PNG, TIFF, WebP, etc. itself.

Step 5: Scaling

Instead of Pillow's

im.thumbnail((1400,900))

Qt does

pixmap = pixmap.scaled(
    self.imageLabel.size(),
    Qt.KeepAspectRatio,
    Qt.SmoothTransformation,
)

The nice thing is that when the window resizes you can automatically rescale.

Step 6: Keyboard shortcuts

Instead of

root.bind("<Right>", ...)

Qt uses

def keyPressEvent(self, event):
    if event.key() == Qt.Key_Right:
        ...

or

QShortcut(...)

Both are straightforward.

I'd actually reorganize the program

Instead of a script, I'd build one class.

BirdBrowser
│
├── load_images()
├── show_image()
├── next_image()
├── previous_image()
├── move_image()
├── delete_image()
├── rename_image()
└── keyPressEvent()

Then your main() becomes tiny:

app = QApplication(sys.argv)

browser = BirdBrowser(pattern="red-tailed-woodpecker")

browser.show()

app.exec()
Version 2

Then I'd add

QMainWindow
│
├── QListWidget
│      thumbnail list
│
└── QLabel
       current image

Like this:

+-----------------------------------------------+
| thumbs |                                      |
|        |                                      |
|        |              image                   |
|        |                                      |
|        |                                      |
+-----------------------------------------------+
| filename                          4032×3024   |
+-----------------------------------------------+

You can click any thumbnail.

Arrow keys update both panes.

That's maybe 40 lines of code with Qt.

Version 3

Then I'd replace QLabel with a QGraphicsView.

That immediately gives you

smooth zoom
pan
wheel zoom
huge images
fit-to-window

with almost no extra code.

This is the architecture I'd use
bird_browser/
│
├── main.py
├── browser.py
├── image_model.py
├── thumbnail_model.py
├── move_dialog.py
├── exif.py
└── utils.py

It sounds like a lot, but each file stays small and focused. As features grow (moving images, EXIF, duplicate detection, species folders), you won't end up with one giant 1,500-line script.

I also have one suggestion that will make the application feel much more polished from the start: use Qt's Model/View framework for the image list instead of manually managing a Python list. It makes thumbnail generation, sorting, filtering, and future features like searching or grouping much easier. For a photo organizer that may eventually handle tens of thousands of images, it's a solid foundation without adding much complexity.