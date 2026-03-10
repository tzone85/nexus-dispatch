package state

// ProjectionStore materializes events into queryable domain models.
type ProjectionStore interface {
	Project(evt Event) error
	GetRequirement(id string) (Requirement, error)
	GetStory(id string) (Story, error)
	ListStories(filter StoryFilter) ([]Story, error)
	Close() error
}
