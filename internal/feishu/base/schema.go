package base

import "context"

// TableField is read-only metadata for checking a business profile before use.
type TableField struct {
	Name           string `json:"name"`
	Type           int    `json:"type"`
	RelatedTableID string `json:"related_table_id,omitempty"`
}

// InspectFields uses the same app identity and paginated schema reader as writes.
// It does not read records, download attachments or mutate a resource.
func (c *LedgerClient) InspectFields(ctx context.Context, base, table string) (map[string]TableField, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	fields, err := c.fieldSchema(ctx, token, base, table)
	if err != nil {
		return nil, err
	}
	result := make(map[string]TableField, len(fields))
	for id, field := range fields {
		result[id] = TableField{Name: field.Name, Type: field.Type, RelatedTableID: field.RelatedTableID}
	}
	return result, nil
}
