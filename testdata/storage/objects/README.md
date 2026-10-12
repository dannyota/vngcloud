# Synthetic object response

`error.xml` is a hand-written adversarial S3 XML response. It is synthetic,
not a GreenNode capture. Public PUT and GET tests decode the response and check
that only the allowlisted code and local safe message reach callers. The other
fields contain synthetic markers that must stay private.
