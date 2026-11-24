# MP3META
[![Coverage Status](https://coveralls.io/repos/github/gcottom/mp3meta/badge.svg?branch=main)](https://coveralls.io/github/gcottom/mp3meta?branch=main)

MP3Meta reads and writes MP3 metadata as a standalone library and as the MP3 adapter for AudioMeta v3. The legacy parser uses Bogem's MIT-licensed id3v2 package.

## Acknowledgements
- [id3v2](https://github.com/bogem/id3v2/v2) - Bogem's id3v2 library is the magic behind the mp3 portion of AudioMeta v3. Thank you Bogem for this amazingly useful lib.

## License
This project is licensed under the MIT License. See the LICENSE file for details.

Parts of this project include third-party libraries under the MIT license. See the LICENSE file for details.

## Related Links
[audiometa v3](https://github.com/gcottom/audiometa/v3)

[mp4meta](https://github.com/gcottom/mp4meta)

[oggmeta](https://github.com/gcottom/oggmeta)

[flacmeta](https://github.com/gcottom/flacmeta)

## Canonical metadata

`OpenMetadata` opens an MP3 as an AudioMeta editor. It accepts a caller-owned
`io.ReadSeeker` and leaves that reader open. The editor supports typed
canonical fields, ordered values, and case-insensitive custom keys.

```go
editor, err := mp3meta.OpenMetadata(input)
if err != nil {
    return err
}
if err := editor.SetField(core.Title, core.TextValue("Example")); err != nil {
    return err
}
if err := editor.SetCustom("COLLECTION", "Favorites", "Archive"); err != nil {
    return err
}
return editor.Save(output)
```

The example imports `github.com/gcottom/mp3meta` and
`github.com/gcottom/audiometa-core` as `core`. The AudioMeta field registry
documents mappings for ID3v2.2, v2.3, and v2.4. An untagged source uses v2.4.
ID3v1/v1.1 trailer values are read at lower precedence. Canonical writes go to
ID3v2, and explicit removals also clear matching legacy trailer values.

- Edits copy the original compressed audio and non-ID3 tail bytes. Recognized
  and unknown leading frames keep their order, headers, and payloads until an
  edit targets them. An unmodified save can copy the complete original file.
- APIC and PIC preserve encoded picture bytes, role, description, and MIME
  where the format can represent them. For a matching picture with dimensions
  or depth, AudioMeta also keeps the extra attributes in a lossless companion
  record. A shape native ID3 cannot represent uses the documented fallback.
- SYLT stores millisecond lyric cues with language and description. CHAP
  supports uncomplicated chapters with millisecond times, a title, and no
  absolute byte offsets. Complex chapter tables and structured frames are
  preserved in place. An edit that would invalidate a table or move encoded
  offsets returns `ErrUnsafeID3Edit`.
- Tags with unsupported revisions or tag-level flags and appended ID3v2.4
  tags can be copied unchanged. An edit returns `ErrUnsafeID3Edit`. Flagged
  target frames and unknown frames marked discard-on-tag-alter also require
  explicit handling. APEv2 and other tail formats are outside the editor's
  clear scope.
- A fallback uses TXXX or TXX with description `audiometa/1/<field>`. Custom
  descriptions use `audiometa/1/custom/<KEY>`. v2.4 supports native terminated
  text lists. v2.2 and v2.3 private lists use `array1:` followed by unpadded
  base64url of a UTF-8 JSON string array. This envelope also escapes a
  singleton starting with the reserved prefix. Structured values use the
  AudioMeta core wire encoding before storage in the ID3 text frame.

`ParseMP3` and `MP3Tag.Save` remain available to existing callers. They use the
legacy v2.3/v2.4 scalar projection, including a single legacy cover art view.
Their save path patches changed frames and retains untouched artwork and
opaque frames. The legacy cover setter encodes an explicitly supplied image
as JPEG. Use the canonical artwork API for an original-byte representation
and multiple roles.

`Save` streams the media bytes to its destination. Passing an `*os.File`
stages output on disk before truncating the destination, including same-file
saves. The final write is non-atomic and may leave partial output on failure.
The reader and writer stay open and the editor is for sequential use.

`go test ./...` runs the shared contract on each supported ID3v2 version,
plus format-specific checks for byte preservation, native frames, fallback,
same-file writes, and unsafe edits.
