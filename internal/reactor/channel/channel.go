package channel

type Channel struct {
}

// ==================================== ready ====================================

func (c *Channel) hasReady() bool {
	return false
}

func (c *Channel) ready() []Action {
	return nil
}

// ==================================== step ====================================

func (c *Channel) step(a Action) error {
	return nil
}

func (c *Channel) stepLeader(a Action) error {
	return nil
}

func (c *Channel) stepFollower(a Action) error {
	return nil
}

// ==================================== tick ====================================

func (c *Channel) tick() {
}

func (c *Channel) tickLeader() {

}

func (c *Channel) tickFollower() {

}

// ==================================== send ====================================

// ==================================== become ====================================

// ==================================== other ====================================
