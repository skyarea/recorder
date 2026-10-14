// The postMessage protocol between the app shell (host) and the pdf.js
// viewer iframe. Both sides import this file, so a message name can never be
// renamed on one side only.
export const VIEWER_SRC = "recorder";
export const HOST_SRC = "host";

export const Msg = {
  Ready: "ready", // viewer -> host: listeners attached, goto accepted
  Pages: "pages", // viewer -> host: document has this many pages
  Page: "page", // viewer -> host: this page is now on screen
  Goto: "goto", // host -> viewer: show this page
};

// The viewer and the host are same-origin documents, so both directions name
// an explicit target origin instead of "*".
export function postToHost(type, fields = {}) {
  parent.postMessage({ src: VIEWER_SRC, type, ...fields }, window.location.origin);
}

export function postToViewer(iframe, type, fields = {}) {
  iframe.contentWindow?.postMessage(
    { src: HOST_SRC, type, ...fields },
    window.location.origin
  );
}

// listener returns an event handler that ignores anything that is not a
// well-formed message from the source we expect.
export function listener(source, handler) {
  return (event) => {
    if (event.origin !== window.location.origin) return;
    const data = event.data;
    if (!data || data.src !== source || typeof data.type !== "string") return;
    handler(data);
  };
}
