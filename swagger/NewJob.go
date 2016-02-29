package swagger

import (
)

type NewJob struct {
    Name  string  `json:"name,omitempty"`
    Image  string  `json:"image,omitempty"`
    Payload  string  `json:"payload,omitempty"`
    Delay  int  `json:"delay,omitempty"`
    Timeout  int  `json:"timeout,omitempty"`
    Retries  int32  `json:"retries,omitempty"`
    
}
