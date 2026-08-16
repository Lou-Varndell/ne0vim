import sys
from pathlib import Path
from send2trash import send2trash

from PySide6.QtCore import Qt, Signal
from PySide6.QtGui import QPixmap
from PySide6.QtWidgets import QLabel, QMainWindow, QVBoxLayout, QWidget, QApplication


ROOT = Path.cwd()  # Path.home() / "Images"


class PathLabel(QLabel):
    doubleClicked = Signal()

    def mouseDoubleClickEvent(self, event):
        if event.button() == Qt.LeftButton:
            self.doubleClicked.emit()
        super().mouseDoubleClickEvent(event)


class ImageBrowser(QMainWindow):
    def __init__(self, root: Path, pattern: str):
        super().__init__()
        self.setWindowTitle("Image Browser")
        self.resize(1000, 700)

        self.images = self.find_matches(root, pattern)
        self.index = 0

        self.image_label = QLabel(alignment=Qt.AlignCenter)
        self.status_label = PathLabel(alignment=Qt.AlignCenter)

        font = self.status_label.font()
        font.setPointSize(24)
        self.status_label.setFont(font)

        self.status_label.setTextInteractionFlags(
            Qt.TextSelectableByMouse | Qt.TextSelectableByKeyboard
        )
        self.status_label.doubleClicked.connect(self.copy_current_path)

        container = QWidget()
        layout = QVBoxLayout(container)
        layout.addWidget(self.image_label)
        layout.addWidget(self.status_label)
        self.setCentralWidget(container)

        if self.images:
            self.show_image()
        else:
            self.status_label.setText(f"No matches for '{pattern}' under {root}")

    def copy_current_path(self):
        path = self.images[self.index]
        QApplication.clipboard().setText(str(path))

    def find_matches(self, root: Path, pattern: str) -> list[Path]:
        pattern = pattern.lower()
        return [p for p in root.rglob("*") if p.is_file() and pattern in str(p).lower()]

    def trash_current_image(self):
        if not self.images:
            return

        path = self.images[self.index]

        try:
            send2trash(str(path))
        except Exception as e:
            self.status_label.setText(f"Failed to trash:\n{e}")
            return

        # Remove from our list
        del self.images[self.index]

        if not self.images:
            self.image_label.clear()
            self.status_label.setText("No images remaining.")
            return

        # Keep index valid
        if self.index >= len(self.images):
            self.index = len(self.images) - 1

        self.show_image()

    def show_image(self):
        path = self.images[self.index]
        pixmap = QPixmap(str(path))
        scaled = pixmap.scaled(
            self.image_label.size(),
            Qt.KeepAspectRatio,
            Qt.SmoothTransformation,
        )
        self.image_label.setPixmap(scaled)
        self.status_label.setText(
            f"{self.index + 1}/{len(self.images)}   "
            f"{pixmap.width()}x{pixmap.height()}\n{path}"
        )

    def next_image(self):
        if self.index < len(self.images) - 1:
            self.index += 1
            self.show_image()

    def previous_image(self):
        if self.index > 0:
            self.index -= 1
            self.show_image()

    def resizeEvent(self, event):
        super().resizeEvent(event)
        if self.images:
            self.show_image()

    def keyPressEvent(self, event):
        if event.key() == Qt.Key_Right:
            self.next_image()

        elif event.key() == Qt.Key_Left:
            self.previous_image()

        elif event.key() in (Qt.Key_Delete, Qt.Key_Backspace):
            self.trash_current_image()

        elif event.key() == Qt.Key_T:
            self.trash_current_image()

        elif event.key() in (Qt.Key_Escape, Qt.Key_Q):
            self.close()

        else:
            super().keyPressEvent(event)


def main():
    pattern = sys.argv[1] if len(sys.argv) > 1 else ""

    app = QApplication(sys.argv)
    browser = ImageBrowser(ROOT, pattern)
    browser.show()
    app.exec()


if __name__ == "__main__":
    main()
