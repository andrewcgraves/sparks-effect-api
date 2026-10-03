package postgres

func SetPublishAfterDecide(fn func()) { publishAfterDecide = fn }

const FindReusableRoutingJobSQL = findReusableRoutingJobSQL
