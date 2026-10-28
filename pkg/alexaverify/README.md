# alexaverify

Verifies that an inbound HTTP request really was sent by Alexa: the request
timestamp is fresh, and `Signature-256` is a valid signature over the exact body
bytes, made by an Amazon certificate fetched from an approved
`SignatureCertChainUrl`.

It is a Go port of the request verification in two of Amazon's own Alexa
Skills Kit SDKs (Java and Node.js). Where they disagree, it takes the stricter
behaviour. It depends only on the standard library.

```
go get github.com/jamestelfer/billy/pkg/alexaverify
```

This is a separate module from [billy](https://github.com/jamestelfer/billy),
the Alexa skill it was written for, so depending on it pulls in none of billy's
dependencies.

## Usage

Read the body once, verify that buffer, then decode the envelope from the same
buffer. Decoding and re-encoding the JSON first changes the bytes and every
signature fails.

```go
verifier, err := alexaverify.New()
if err != nil {
	return err
}

http.HandleFunc("/alexa", func(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := verifier.Verify(r.Context(), body, r.Header); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// body is authentic: decode it and handle the request.
})
```

`Verifier.Warm` seeds the certificate cache at startup. It is best effort: log
a failure and carry on. Every error `Verify` returns wraps exactly one of the
package's exported sentinels, so callers can tell failures apart with
`errors.Is`.

There is no option that turns verification off, and certificate revocation is
deliberately not checked, matching both reference SDKs. The package
documentation covers both decisions, the check order and the certificate cache.

## Versioning

The exported API is a semver contract. Releases are tagged
`pkg/alexaverify/vX.Y.Z`; see `CHANGELOG.md`.

## License

Apache-2.0. See `LICENSE` and `NOTICE` for attribution of the ported Amazon SDK
code.
