package dbclient

import (
	"encoding/csv"
	"encoding/json"
	"io"
)

func Export(output io.Writer, result Result, extension string) error {
	switch extension {
	case "csv":
		writer := csv.NewWriter(output)
		if err := writer.Write(result.Columns); err != nil {
			return err
		}
		for _, cells := range result.Rows {
			row := make([]string, len(cells))
			for index, value := range cells {
				if !value.Null {
					row[index] = value.Text
				}
			}
			if err := writer.Write(row); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	case "json":
		rows := make([][]any, 0, len(result.Rows))
		for _, cells := range result.Rows {
			row := make([]any, len(cells))
			for index, value := range cells {
				if !value.Null {
					row[index] = value.Text
				}
			}
			rows = append(rows, row)
		}
		return json.NewEncoder(output).Encode(struct {
			Columns []string `json:"columns"`
			Rows    [][]any  `json:"rows"`
		}{result.Columns, rows})
	default:
		return ErrInput
	}
}
