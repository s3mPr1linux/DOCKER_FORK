package libnet

type Config struct {
	repo *git.Repo
	branch string	// The branch name
	subtree string	// A path relative to t, under which the config is scoped
	t *git.Commit   // The current snapshot
}

func (j *Config) Snapshot(hash string) (*Config, error) {

}

func (j *Config) Get(hash string) (*Tree, error) {

}

func (j *Config) Commit(desc []string, t *Tree) (string, error) {

}

type Config interface {
	// Reset uncommitted changes
	Reset()

	// Return a duplicate config, with uncommitted changes reset
	Clone() Config

	// Return the specified sub-tree, creating it if needed
	Subtree(string) (Config, error)

	GetBlob(string) (string, error)
	GetBlobDefault(string, string) (string, error)

	SetBlob(string, string) error

	Commit(ConflictHandler) error

	// Return a hash of the state of the commited config.
	// Identical configs always have identical hashes.
	// Different configs always have different hashes.
	//
	// Note: this is the hash of the config sub-tree, NOT the top-level tree
	// and NOT the commit.
	//
	Hash() string

	// Change the config to point to the previous committed version. Uncommitted changes are preserved.
	Prev() error

	// Change the config to point to the latest committed version (ie the HEAD of the branch). Uncommitted changes are preserved.
	Update() error
}

// FIXME: allow the conflict handler to specify retries
type ConflictHandler func(mine, other Config) error
