package controller

import (
	"encoding/json"
	"fmt"
)

type Data struct {
	Packages []*Package `json:"packages"`
}

type Package struct {
	Name string `json:"name"`
}

func (c *Controller) writeData(path string, data *Data) error {
	f, err := c.fs.Create(path)
	if err != nil {
		return fmt.Errorf("create a data file: %w", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(data); err != nil {
		return fmt.Errorf("write data to a file: %w", err)
	}
	return nil
}

func (c *Controller) readData(path string, data *Data) error {
	f, err := c.fs.Open(path)
	if err != nil {
		return fmt.Errorf("open a data file: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(data); err != nil {
		return fmt.Errorf("read a data file as JSON: %w", err)
	}
	return nil
}
