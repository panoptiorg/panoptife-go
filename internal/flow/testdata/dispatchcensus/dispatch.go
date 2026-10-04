// Package dispatchcensus is a minimal fixture for the flow.Build census
// counters (CappedSites / UnresolvedDynamicSites): one interface-invoke call
// site and one dynamic func-value call site, each routed through
// Dispatcher.TargetsAt exactly once.
package dispatchcensus

type Runner interface{ Run() }

func CallBoth(r Runner, f func()) {
	r.Run()
	f()
}
