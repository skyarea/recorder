// throttle paces a function that fires too often: scrolling a PDF raises a
// page-change for every page it crosses, and each one would be a write to the
// database. This wraps such a function so it runs at most once per window,
// while the very first call is not delayed and the last call in a burst is
// never dropped.
//
// Two behaviours the reader relies on:
//   - leading: the first call runs immediately, so the current page shows up
//     without waiting out the window;
//   - trailing: if more calls arrive before the window closes, one final call
//     runs at the window's edge with the most recent arguments — so the saved
//     progress ends on the page the reader actually stopped on, not the first
//     one of the burst. A lone call produces no trailing call: there is
//     nothing new to flush.
//
// It takes only (fn, ms): the reader's own "which file" argument is just data
// the wrapped function receives, so nothing here knows about the API.

// A timer handle must not hold the event loop open (the tests exit while a
// window is still pending); in the browser setTimeout is the only thing this
// uses, and there is nothing to unref.
export function throttle(fn, ms) {
  let timer = null;
  // The arguments of the most recent call held for the trailing edge, or null
  // when nothing is pending.
  let pending = null;

  const runTrailing = () => {
    timer = null;
    const args = pending;
    pending = null;
    if (args) fn(...args);
  };

  const wrapper = function throttled(...args) {
    if (timer === null) {
      // Leading edge: the window is open from this call, so fire now and arm
      // the trailing flush at its far end.
      fn(...args);
      timer = setTimeout(runTrailing, ms);
      timer.unref?.();
    } else {
      // Inside an open window: keep only the newest arguments for the flush.
      pending = args;
    }
  };

  // cancel forgets anything held for the trailing edge and closes the window,
  // so the next call leads again. The reader calls it when the open document
  // changes: a flush armed under the old file would save that page onto the
  // new one.
  wrapper.cancel = () => {
    if (timer !== null) clearTimeout(timer);
    timer = null;
    pending = null;
  };

  return wrapper;
}
