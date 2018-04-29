package service

type DemoServiceAPI struct {
	service *DemoService
}

func newDemoServiceAPI(s *DemoService) *DemoServiceAPI {
	return &DemoServiceAPI{
		service: s,
	}
}

func (self *DemoServiceAPI) Submit(data []byte, difficulty uint8) (uint64, error) {
	return self.service.submitRequest(data, difficulty)
}
