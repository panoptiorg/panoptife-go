// Package endofday mirrors acme's EOD orchestration: one IStep interface,
// one impl per step package. Ranging over []IStep is a 4-way dispatch site
// (confidence 1/4 under vta/cha).
package endofday

type IStep interface {
	Run(date string) error
}
