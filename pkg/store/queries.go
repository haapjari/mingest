package store

import _ "embed"

// The Store's SQL, one statement per file in sql/.
var (
	//go:embed sql/schema.sql
	schema string

	//go:embed sql/progress_columns.sql
	progressColumns string

	//go:embed sql/insert_progress.sql
	insertProgressSet string

	//go:embed sql/update_opened.sql
	updateOpenedSet string

	//go:embed sql/update_finish.sql
	updateFinishSet string

	//go:embed sql/insert_message.sql
	insertMessage string

	//go:embed sql/upsert_sender.sql
	upsertSender string

	//go:embed sql/update_progress.sql
	updateProgressSet string

	//go:embed sql/select_top_senders.sql
	selectTopSenders string
)

// The statements that read or write the progress row return it, with
// progressColumns in the order scanStatus reads them.
var (
	selectProgress = "SELECT\n" + progressColumns + "FROM ingestion"
	insertProgress = insertProgressSet + "RETURNING\n" + progressColumns
	updateOpened   = updateOpenedSet + "RETURNING\n" + progressColumns
	updateFinish   = updateFinishSet + "RETURNING\n" + progressColumns
	updateProgress = updateProgressSet + "RETURNING\n" + progressColumns
)
