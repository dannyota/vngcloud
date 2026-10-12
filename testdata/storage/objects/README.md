# Synthetic object responses

`error.xml` is a hand-written adversarial S3 XML response. It is synthetic,
not a GreenNode capture. Public PUT and GET tests decode the response and check
that only the allowlisted code and local safe message reach callers. The other
fields contain synthetic markers that must stay private.

`list.xml` and `list-empty.xml` are hand-written synthetic raw ListObjectsV2
responses based on the accepted S3 wire shape. They are not GreenNode captures
or live evidence. Public ListObjects tests decode the ordinary grouped,
truncated page and the empty page without KeyCount. The ordinary fixture
includes URL-encoded names, XML-escaped opaque token text, optional metadata,
and an unknown Owner subtree.

`delete_204.http` stores a hand-written synthetic raw HTTP 204 response as a
JSON-quoted string based on the accepted S3 DELETE wire shape. Escaped CRLF
bytes preserve the complete header terminator without a blank line at the
file's end. It is not a GreenNode capture or live evidence. Public DeleteObject
tests unquote the string, verify complete bodyless framing, parse the raw
response through the SDK transport, and check optional marker/version
observations. The fixture proves client header handling, not prior existence,
permanent deletion, or provider versioning behavior.

`copy_result.xml` and `copy_error.xml` are hand-written synthetic raw S3 XML
responses based on the accepted native copy wire shape. They are not GreenNode
captures or live evidence. Public CopyObject tests decode a complete result and
an embedded Error under HTTP 200. The result includes an escaped ETag, an offset
timestamp, and an unknown subtree. The error includes synthetic private markers
that must never reach callers. These fixtures prove local result handling, not
provider metadata preservation, encryption, versioning, or source snapshots.
