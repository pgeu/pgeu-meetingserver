package main

type Poll struct {
	Question string
	Answers  []string
	votes    map[int]int
}

func NewPoll(question string, answers []string) *Poll {
	return &Poll{
		Question: question,
		Answers:  answers,
		votes:    make(map[int]int),
	}
}

func (p *Poll) VoteCount() int {
	return len(p.votes)
}

func (p *Poll) Tally() [5]int {
	tally := [5]int{0, 0, 0, 0, 0}
	for _, v := range p.votes {
		tally[v]++
	}
	return tally
}

func (p *Poll) Voted() []int {
	var voted []int
	for k := range p.votes {
		voted = append(voted, k)
	}
	if voted == nil {
		return make([]int, 0)
	}
	return voted
}

func (p *Poll) CastVote(userid int, vote int) bool {
	_, already := p.votes[userid]
	p.votes[userid] = vote
	return already
}
