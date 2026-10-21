// Left-edge flyout panel: hovering the mouse into the far-left strip of
// the viewport slides the panel out; moving past its width slides it back
// in. The panel's content is lazy-loaded over htmx the first time it opens
// for a given directory, keyed off whichever on-page element currently
// carries a data-dir attribute (see layout.templ).
(function () {
	var panel = document.getElementById("flyout-panel");
	if (!panel) {
		return;
	}

	var EDGE_PX = 12;

	var loadedForDir = null;

	function currentDir() {
		var host = document.querySelector("[data-dir]");
		return host ? host.dataset.dir : "";
	}

	function open() {
		var dir = currentDir();
		if (loadedForDir !== dir) {
			htmx.ajax("GET", "/catalog/flyout?dir=" + encodeURIComponent(dir), {
				target: "#flyout-panel",
				swap: "innerHTML",
			});
			loadedForDir = dir;
		}
		panel.classList.add("is-open");
	}

	function close() {
		panel.classList.remove("is-open");
	}

	document.addEventListener("mousemove", function (e) {
		if (e.clientX <= EDGE_PX) {
			open();
			// The panel's layout box (and thus offsetWidth) is unaffected by
			// the CSS transform that hides it, so this threshold holds
			// whether the panel is open or still animating closed.
		} else if (e.clientX > panel.offsetWidth) {
			close();
		}
	});

	// A directory change or an applied filter swaps #catalog-content
	// (or, on the image viewer, never — but that page never swaps
	// anyway). Invalidate the cache so the next edge-hover re-fetches
	// fresh aggregates instead of showing a stale directory's.
	document.body.addEventListener("htmx:afterSwap", function (e) {
		if (e.detail.target && e.detail.target.id === "catalog-content") {
			loadedForDir = null;
		}
	});
})();
