# `minidex`

`minidex` is both a dex testing and prototyping model, and a potential light-
weight Stream Deck controller. It does not have the full features of `dex`,
such a live configuration reloading and validation, and safe inter-plugin
communication, but it is simpler to debug and understand, and runs in a
single process. It does retain the persistent state features provided by
`dex`.

Plugins make direct use of internal dex API which has no stability guarantee.

Functionally it abuses the Go build chain to use Go code as a configuration
language. This is feasible because of the rapid builds that Go provides.