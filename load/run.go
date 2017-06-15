package load

// Load : interface should be implement
type Load interface {
	do()
}

// Run load test
func Run(ins Load) {
	ins.do()
}
