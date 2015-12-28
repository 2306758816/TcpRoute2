package main

import (
	"time"
	"net"
	"github.com/golang/glog"
	"fmt"
	"io"
)

//
const (
// 尝试创建处理器时的conn.read 的timeout
// 实际是一次性读取数据，所以这个超时指的是客户端必须10秒内发出第一和数据
	handlerNewTimeout = 10 * time.Second

// 默认一个连接的总处理时间，一般都会被实际的处理器修改掉。
	handlerBaseTimeout = 10 * time.Minute
)

type Server struct {
	Addr     string          // TCP 监听地址
	hNewer   HandlerNewer    // 请求处理器
	upStream UpStreamDial    // 上层代理
	ln       net.Listener
	errConn  *ErrConnService //错误连接统计
}

func NewServer(addr string) *Server {
	srv := Server{}
	srv.Addr = addr

	// 错误连接记录
	srv.errConn = NewErrConnService()


	// 处理器
	h := NewSwitchHandlerNewer()
	hs := NewSocksHandlerNewer(srv.upStream)
	h.AppendHandlerNewer(hs)
	srv.hNewer = h

	// 基本上层代理
	upStream, err := NewBaseUpStream(&srv)
	if err != nil {
		panic(err)
	}
	srv.upStream = upStream

	return &srv

}

func (srv *Server) ListAndServe() error {
	if srv.Addr == "" {
		srv.Addr = ":7070"
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		panic(err)
	}
	srv.ln = ln

	return srv.Server()
}


func (srv *Server) Server() error {
	ln := srv.ln
	defer ln.Close()
	var tempDelay time.Duration
	for {
		rw, e := ln.Accept()
		if e != nil {
			if ne, ok := e.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				glog.Warning("Accept error: %v; retrying in %v", e, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return e
		}
		tempDelay = 0

		go srv.handlerConn(rw)
	}
}

func (srv *Server) handlerConn(conn net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			glog.Error("work failed:", err)
		}
	}()
	// 是这里调用关闭还是 Handler() 负责？
	defer conn.Close()

	if tcpConn, ok := conn.(*net.TCPConn); ok == true {
		// 设置关闭连接时最多等待多少秒
		tcpConn.SetLinger(5)
	}
	conn.SetDeadline(time.Now().Add(handlerNewTimeout))

	h, _, err := srv.hNewer.New(conn)
	if h == nil {
		glog.Warning(fmt.Sprintf("无法识别请求的协议类型，远端地址：%v，近端地址：%v，详细错误：%v", conn.RemoteAddr(), conn.LocalAddr(), err))
		return
	}

	conn.SetDeadline(time.Now().Add(handlerBaseTimeout))
	if err := h.Handle(); err != nil {
		if err != io.EOF {
			glog.Warning("协议处理错误：", err)
		}
	}
}
