// Package other holds a stream-shaped interface OUTSIDE the pb package the
// server embeds from — handlerKind must reject methods using it.
package other

type Feed_EvilServer interface {
	Send(*int) error
}
