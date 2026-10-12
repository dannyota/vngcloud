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
