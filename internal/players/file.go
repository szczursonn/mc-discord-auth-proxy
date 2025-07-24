package players

import (
	"encoding/csv"
	"fmt"
	"os"

	"github.com/disgoorg/snowflake/v2"
)

func readPlayerConfigurationFile(fileName string) (map[string]snowflake.ID, error) {
	f, err := os.OpenFile(fileName, os.O_RDONLY|os.O_CREATE, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read csv file: %w", err)
	}

	usernameToUserId := make(map[string]snowflake.ID, len(rows))
	for i, row := range rows {
		if err := func() error {
			if len(row) < 2 {
				return fmt.Errorf("too few columns in row")
			}

			userId, err := snowflake.Parse(row[1])
			if err != nil {
				return fmt.Errorf("invalid user id: %w", err)
			}

			usernameToUserId[row[0]] = userId

			return nil
		}(); err != nil {
			return nil, fmt.Errorf("at row %d: %w", i, err)
		}
	}

	return usernameToUserId, nil
}
