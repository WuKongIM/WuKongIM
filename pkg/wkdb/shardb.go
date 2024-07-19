package wkdb

// type shardb struct {
// 	*pebble.DB
// 	stopper        *syncutil.Stopper
// 	appendMessageC chan AppendMessagesReq

// 	wkdb DB
// }

// func newShardb(db *pebble.DB, wkdb DB) *shardb {
// 	return &shardb{
// 		DB:             db,
// 		appendMessageC: make(chan AppendMessagesReq, 1024),
// 		stopper:        syncutil.NewStopper(),
// 		wkdb:           wkdb,
// 	}
// }

// // 异步增加消息
// func (s *shardb) appendMessageAsync(req AppendMessagesReq) {
// 	select {
// 	case s.appendMessageC <- req:
// 	case <-s.stopper.ShouldStop():
// 		return
// 	}
// }

// func (s *shardb) writeLoop() {
// 	for {
// 		select {
// 		case req := <-s.appendMessageC:

// 		case <-s.stopper.ShouldStop():
// 			return
// 		}
// 	}
// }
