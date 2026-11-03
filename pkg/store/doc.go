// Package store keeps Ingestions in SQLite behind the ingest store port: a
// database file per ingestion, one transaction per chunk together with its
// progress, a free-space check before each commit, and the top senders query.
package store
