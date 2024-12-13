package reactor

var User *UserPlus
var Channel IChannel

func RegisterUser(u IUser) {
	User = &UserPlus{
		user: u,
	}
}

func RegisterChannel(c IChannel) {
	Channel = c
}
