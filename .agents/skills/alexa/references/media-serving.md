# Serving audio

Alexa fetches media as a separate HTTP transaction with no signature headers. Authenticate
`/alexa` only — applying that middleware to the media route breaks playback.

For a static MP3 endpoint:

- set `Content-Type: audio/mpeg` explicitly;
- support GET and HEAD;
- support byte ranges with coherent `206`, `Content-Range`, and `Content-Length`;
- return `416` for an unsatisfiable range;
- keep the route fixed — no URL, query, or header input becoming a filesystem path;
- serve a publicly reachable HTTPS certificate that Alexa trusts.

Go's `http.ServeContent` handles ranges and conditional requests given an `io.ReadSeeker`; set
`Content-Type` before calling it.

Validate configured media from disk before opening the public listener. Resolve it relative to
an operator-controlled descriptor directory and block lexical and symlink escape — `os.Root`
follows links inside the root while rejecting links that resolve outside it.

A physical Echo is the only real test of URL reachability and MP3 encoding compatibility.
