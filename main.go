package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Backend struct{
	URL *url.URL
	Alive bool
	mux sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}



//SETALIVE
func (b* Backend)SetAlive(alive_status bool){
	b.mux.Lock()
	b.Alive=alive_status
	b.mux.Unlock()
}

//ISALIVE
func (b * Backend)IsAlive() (alive bool){
	b.mux.Rlock()
	alive=b.alive
	b.mux.RUnlock()
	return
}

type serverPool struct{
	backends []*Backend
	current uint64
}

//AddBackend
func(s* serverPool) AddBackend(new_backend_url string){
	s.backends=append(s.backends,new_backend_url)
}

//NextIndex
func(s* serverPool) NextIndex() int{
	return int(atomic.Addint(uint64(s.current),uint64(1))%uint64(len(s.backends)))
}

func (s* serverPool) GetNextPeer() *Backend{
	next=s.NextIndex()
	l:=len(s.backends)+next
	for i:=next;i<l;i++{
		idx=i%len(s.backends)
		if s.backends[idx].isAlive(){
			if i!=next{
				atomic.StoreUint64(&s.current,uint64(idx))
			}
			return s.backends[idx]
		}
	}
	return nil

}
func main(){

}