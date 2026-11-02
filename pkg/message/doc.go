// Package message holds the Message rules that turn mail files into
// messages: it scans a mail file's header fields and body, and identifies
// its message by a content fingerprint, so copies collapse into one,
// together with the message's sender. It is pure and does no I/O of its
// own.
package message
