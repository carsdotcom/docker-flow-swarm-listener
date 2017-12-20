package main

import (
	"time"

	"./metrics"
	"./service"
)

func main() {
	logPrintf("Starting Docker Flow: Swarm Listener")
	s := service.NewServiceFromEnv()
	n := service.NewNotificationFromEnv()
	bigIp := NewBigIpFromEnv()
	serve := NewServe(s, n)
	go serve.Run()

	args := getArgs()
	if len(n.CreateServiceAddr) > 0 {
		logPrintf("Starting iterations")
		for {
			allServices, err := s.GetServices()
			if err != nil {
				metrics.RecordError("GetServices")
			}
			newServices, err := s.GetNewServices(allServices)
			if err != nil {
				metrics.RecordError("GetNewServices")
			}
			err = n.ServicesCreate(
				newServices,
				args.Retry,
				args.RetryInterval,
			)
			if err != nil {
				metrics.RecordError("ServicesCreate")
			}
			bigIp.AddRoutes(newServices)
			removedServices := s.GetRemovedServices(allServices)
			err = n.ServicesRemove(removedServices, args.Retry, args.RetryInterval)
			metrics.RecordService(len(service.CachedServices))
			if err != nil {
				metrics.RecordError("ServicesRemove")
			}
			bigIp.RemoveRoutes(removedServices)
			time.Sleep(time.Second * time.Duration(args.Interval))
		}
	}
}
