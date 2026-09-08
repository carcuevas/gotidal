package visualizer

// Broadcast reads from src until it closes and forwards each buffer to every
// dst, unmodified — the tap already hands out a fresh copy per buffer (see
// player.Player.TapPCM), so sharing the same slice across subscribers that
// only ever read it is safe. Sends are non-blocking: a busy or unused
// subscriber only ever drops frames, exactly like the tap's own backpressure
// semantics, so one slow subscriber (e.g. Cava, restarting its subprocess)
// never affects its sibling (e.g. PeakMeter).
func Broadcast(src <-chan []byte, dsts ...chan<- []byte) {
	for buf := range src {
		for _, d := range dsts {
			select {
			case d <- buf:
			default:
			}
		}
	}
}
