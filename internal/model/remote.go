package model

import "time"

type Remote struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Backend       string    `json:"backend"`
	BackendConfig string    `json:"backend_config"`
	ElephantID    string    `json:"elephant_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func EntryNode(p Project, id string) string {
	if p.Scope == "remote" {
		return "entry:" + p.TableName + ":" + id
	}
	return "entry:" + id
}
