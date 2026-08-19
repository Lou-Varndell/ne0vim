#!/usr/bin/env python3

from contextlib import contextmanager
import httpx
import time
from pathlib import Path
from urllib.parse import urlparse, urljoin
from bs4 import BeautifulSoup


IMAGES_DIR = Path.home() / "Images" / time.strftime("%Y%m%d/%H%M%S")
RETRY_STATUS_CODES = {429, 500, 502, 503, 504}
MAX_RETRIES = 5
INPUT_FILE = Path("img-dl.txt")
SRC_ATTRS = [
    "data-lazy-src",
    "data-src",
    "data-original",
    "data-srcset",
    "data-original-src",
    "data-hi-res-src",
    "data-full-src",
    "data-image",
    "srcset",
    "src",
]


# SEEN_FILE = Path("seen-urls.txt")
# IMG_SRC_FILE = Path("img-src.txt")
# A_HREF_FILE = Path("a-href.txt")
# with (
#     httpx.Client(follow_redirects=True, timeout=30) as client,
#     SEEN_FILE.open("a") as f_seen,
#     IMG_SRC_FILE.open("a") as f_src,
#     A_HREF_FILE.open("a") as f_href,
# ):


def create_session():
    """Create an httpx client with retries."""

    headers = {
        "User-Agent": (
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
            "AppleWebKit/537.36 (KHTML, like Gecko) "
            "Chrome/148.0.0.0 Safari/537.36"
        ),
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "Accept-Language": "en-US,en;q=0.5",
    }

    return httpx.Client(
        headers=headers,
        follow_redirects=True,
        timeout=httpx.Timeout(30.0, connect=5.0),
    )


def get(session, url):
    """GET a URL with retries and exponential backoff."""

    for attempt in range(MAX_RETRIES + 1):
        try:
            # , stream=True, timeout=DEFAULT_TIMEOUT
            response = session.get(url)

            if response.status_code not in RETRY_STATUS_CODES:
                return response

            if attempt == MAX_RETRIES:
                continue

            retry_after = response.headers.get("Retry-After")

            if retry_after:
                try:
                    delay = float(retry_after)
                except ValueError:
                    delay = 2**attempt
            else:
                delay = 2**attempt

            time.sleep(delay)

        except (httpx.ConnectError, httpx.ReadError, httpx.TimeoutException):
            if attempt == MAX_RETRIES:
                raise

            time.sleep(2**attempt)

    raise RuntimeError("Unreachable")


@contextmanager
def stream(session, url):
    """Stream a URL with retries and exponential backoff."""

    for attempt in range(MAX_RETRIES + 1):
        try:
            with session.stream("GET", url) as response:
                if response.status_code not in RETRY_STATUS_CODES:
                    yield response
                    return

                if attempt == MAX_RETRIES:
                    response.raise_for_status()

                retry_after = response.headers.get("Retry-After")

                if retry_after:
                    try:
                        delay = float(retry_after)
                    except ValueError:
                        delay = 2**attempt
                else:
                    delay = 2**attempt

        except (httpx.ConnectError, httpx.ReadError, httpx.TimeoutException):
            if attempt == MAX_RETRIES:
                raise

            delay = 2**attempt

        time.sleep(delay)

    raise RuntimeError("Unreachable")


def download(session):
    with INPUT_FILE.open() as f:
        for url in f:
            url = url.strip()
            if not url:
                continue

            parsed = urlparse(url)
            destination = IMAGES_DIR / parsed.netloc / parsed.path.lstrip("/")

            if destination.exists():
                print(f"Skipping {destination}")
                continue

            destination.parent.mkdir(parents=True, exist_ok=True)

            with stream(session, url) as response:
                response.raise_for_status()

                with destination.open("wb") as file:
                    for chunk in response.iter_bytes():
                        file.write(chunk)

            print(destination)


def bs(session):
    links = set()
    imgs = set()

    with INPUT_FILE.open() as f:
        for url in f:
            url = url.strip()
            if not url:
                continue

            r = get(session, url)
            soup = BeautifulSoup(r, "lxml")

            for img in soup.select("img"):
                src = next((img.get(attr) for attr in SRC_ATTRS if img.get(attr)), None)

                if not src:
                    continue

                img_url = urljoin(url, src)
                imgs.add(img_url)
                print(f"img: {img_url}")

            # {a["href"] for a in soup.select("a[href]")}
            for a in soup.select("a"):
                href = a.get("href")
                if not href:
                    continue

                links.add(href)
                print(f"href: {href}")

    with Path("a-href.txt").open("a") as fout:
        for link in sorted(links):
            if "rdcpix" in link:
                fout.write(link + "\n")

    with Path("img-src.txt").open("a") as fout:
        for src in sorted(imgs):
            if "rdcpix" in src:
                fout.write(src + "\n")


def main():
    with create_session as session:
        bs(session)
        # download(session)


if __name__ == "__main__":
    main()
