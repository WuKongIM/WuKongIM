package logicclient

type Client interface {
	Auth(req *AuthReq) (*AuthResp, error)
}
