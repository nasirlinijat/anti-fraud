package models

// BlocklistQuery selects a numbered page of current entries, with optional filters.
type BlocklistQuery struct {
	Type     BlocklistType
	Query    string
	Page     int64
	PageSize int
}

// BlocklistPagination describes the matching dataset and the returned page.
type BlocklistPagination struct {
	Page       int64 `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalPages int64 `json:"total_pages"`
	TotalItems int64 `json:"total_items"`
}

// BlocklistPage contains a bounded page and counts from the same database snapshot.
type BlocklistPage struct {
	Entries    []StoredBlocklistEntry `json:"entries"`
	Pagination BlocklistPagination    `json:"pagination"`
}
