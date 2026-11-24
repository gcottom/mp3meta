# Changelog

## Unreleased

- Added the canonical `OpenMetadata` API for ID3v2.2, v2.3 and v2.4, with lower-precedence reads for ID3v1/v1.1.
- Added native and conventional storage choices, typed fallback fields, multi-valued custom metadata, and limited native artwork, credits, synchronized lyrics, and chapters.
- Preserve untouched frames, compressed audio, and original artwork bytes during metadata edits. Return explicit errors for layouts that cannot safely be rewritten.
- Retain the existing `MP3Tag` API and improve its save path to patch changed frames while preserving unrelated metadata.
- Added tests for audio-byte equality, version coverage, independent track/disc field removal, same-file staging, malformed metadata, and preservation rules.

The original Bogem-based parser and prior release history remain. The changes above are AI-assisted follow-up work on the v0.0.5 baseline.
