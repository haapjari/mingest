// Package ingest is the service core. Its Ingester runs one ingestion at a
// time, resumes an interrupted one, and reports ingestion status and top
// senders. Its Pipeline reads, parses and writes the dataset in chunks. It
// declares the Store port that persistence implements.
package ingest
