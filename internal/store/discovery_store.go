package store

import (
	"context"
	"fmt"
)

// DiscoveryHealthRow is one provider's catalogue, summarised.
//
// MaxMissingStreak is the loud number: a provider whose listing has been
// failing looks identical to a healthy one until something counts the
// consecutive sweeps that omitted its models.
type DiscoveryHealthRow struct {
	ProviderID       string
	Total            int
	Live             int
	Stale            int
	RemovedUpstream  int
	MaxMissingStreak int
	// FilteredOut is how many models the last sweep dropped before recording
	// it. Non-zero only under the free-models filter, and the only thing that
	// distinguishes "this provider serves nothing" from "this provider serves
	// nothing free".
	FilteredOut int
}

func (d *DB) DiscoveryHealth(ctx context.Context) ([]DiscoveryHealthRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		// Driven from the union of both tables, not from either alone.
		//
		// models alone loses a sweep that imported nothing -- every model
		// filtered out -- which then reads as a provider discovery has never
		// visited: a different fact with a different fix. provider_discovery
		// alone loses a provider whose models were written by the config
		// import, which never runs a sweep. A provider in neither table has
		// genuinely never been discovered and is still absent, which is what
		// the caller reads as "never discovered".
		`SELECT p.provider_id,
		        coalesce(count(m.model_id), 0),
		        coalesce(sum(m.state = 'live'), 0),
		        coalesce(sum(m.state = 'stale'), 0),
		        coalesce(sum(m.state = 'removed_upstream'), 0),
		        coalesce(max(m.missing_streak), 0),
		        coalesce(max(d.filtered_out), 0)
		   FROM (SELECT provider_id FROM models
		         UNION
		         SELECT provider_id FROM provider_discovery) p
		   LEFT JOIN models m ON m.provider_id = p.provider_id
		   LEFT JOIN provider_discovery d ON d.provider_id = p.provider_id
		  GROUP BY p.provider_id
		  ORDER BY p.provider_id`)
	if err != nil {
		return nil, fmt.Errorf("discovery health: %w", err)
	}
	defer rows.Close()

	out := []DiscoveryHealthRow{}
	for rows.Next() {
		var r DiscoveryHealthRow
		if err := rows.Scan(&r.ProviderID, &r.Total, &r.Live, &r.Stale,
			&r.RemovedUpstream, &r.MaxMissingStreak, &r.FilteredOut); err != nil {
			return nil, fmt.Errorf("discovery health: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discovery health: %w", err)
	}
	return out, nil
}
