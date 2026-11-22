package loadbalancer

// CreatePoolOutput holds the created pool, including when settling fails.
type CreatePoolOutput struct {
	Pool Pool
}

// UpdatePoolOutput holds the last observed pool, or submitted values with NoWait.
type UpdatePoolOutput struct {
	Pool Pool
}

// DeletePoolOutput confirms the pool delete request succeeded.
type DeletePoolOutput struct{}
