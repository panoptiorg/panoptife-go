package lib

import (
	"log"
	"os/exec"
)

// Cache has two implementations, but NewCache only ever returns one of them.
type Cache interface {
	Get(key string) string
}

type memCache struct{ m map[string]string }

// Get logs key: key reaches a log sink.
func (c *memCache) Get(key string) string {
	log.Println(key)
	return c.m[key]
}

type diskCache struct{ dir string }

// Get reads the file named key with an external command: key reaches an exec sink.
func (c *diskCache) Get(key string) string {
	out, _ := exec.Command("cat", c.dir+"/"+key).Output()
	return string(out)
}

// NewCache returns a Cache. With the body unbuilt, a caller cannot tell which
// implementation that is.
func NewCache() Cache { return &memCache{m: map[string]string{}} }

// NewDiskCache returns the other implementation, so it is used somewhere.
func NewDiskCache(dir string) Cache { return &diskCache{dir: dir} }
