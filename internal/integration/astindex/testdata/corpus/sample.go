package sample

type Widget struct{ ID int }

func (w Widget) Key() int { return w.ID }
