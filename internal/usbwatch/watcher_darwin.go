package usbwatch

import (
	"context"
	"log"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// CF and IOKit type aliases matching usbhid conventions.
type (
	cfAllocatorRef  uintptr
	cfDictionaryRef uintptr
	cfIndex         int64
	cfNumberRef     uintptr
	cfNumberType    = cfIndex
	cfRunLoopRef    uintptr
	cfStringRef     uintptr
	cfTypeRef       uintptr

	cfStringEncoding uint32

	ioHIDDeviceRef  uintptr
	ioHIDManagerRef uintptr
	ioOptionBits    uint32
	ioReturn        int32
)

const (
	kCFAllocatorDefault   cfAllocatorRef   = 0
	kCFNumberSInt16Type   cfIndex          = 2
	kCFNumberSInt32Type   cfIndex          = 3
	kCFStringEncodingUTF8 cfStringEncoding = 0x08000100

	kCFRunLoopRunFinished int32 = 1
	kCFRunLoopRunStopped  int32 = 2

	kIOHIDOptionsTypeNone ioOptionBits = 0
	kIOReturnSuccess      ioReturn     = 0
)

// purego function bindings
var (
	cfDictionaryCreateMutable func(alloc cfAllocatorRef, capacity cfIndex, keyCallBacks, valueCallBacks uintptr) cfDictionaryRef
	cfDictionarySetValue      func(dict cfDictionaryRef, key, value uintptr)
	cfNumberCreate            func(alloc cfAllocatorRef, theType cfNumberType, valuePtr unsafe.Pointer) cfNumberRef
	cfNumberGetValue          func(number cfNumberRef, theType cfNumberType, valuePtr unsafe.Pointer) bool
	cfRelease                 func(cf cfTypeRef)
	cfRunLoopGetCurrent       func() cfRunLoopRef
	cfRunLoopRunInMode        func(mode cfStringRef, seconds float64, returnAfterSourceHandled bool) int32
	cfRunLoopStop             func(runLoop cfRunLoopRef)
	cfStringCreateWithBytes   func(alloc cfAllocatorRef, bytes []byte, numBytes cfIndex, encoding cfStringEncoding, isExternalRepresentation bool) cfStringRef

	objcAutoreleasePoolPush func() uintptr
	objcAutoreleasePoolPop  func(pool uintptr)

	ioHIDDeviceGetProperty                     func(device ioHIDDeviceRef, key cfStringRef) cfTypeRef
	ioHIDManagerClose                          func(manager ioHIDManagerRef, options ioOptionBits) ioReturn
	ioHIDManagerCreate                         func(allocator cfAllocatorRef, options ioOptionBits) ioHIDManagerRef
	ioHIDManagerOpen                           func(manager ioHIDManagerRef, options ioOptionBits) ioReturn
	ioHIDManagerSetDeviceMatching              func(manager ioHIDManagerRef, matching cfDictionaryRef)
	ioHIDManagerRegisterDeviceMatchingCallback func(manager ioHIDManagerRef, callback uintptr, context unsafe.Pointer)
	ioHIDManagerScheduleWithRunLoop            func(manager ioHIDManagerRef, runLoop cfRunLoopRef, runLoopMode cfStringRef)
)

var (
	kCFRunLoopDefaultMode           uintptr
	kCFTypeDictionaryKeyCallBacks   uintptr
	kCFTypeDictionaryValueCallBacks uintptr
)

func init() {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	purego.RegisterLibFunc(&cfDictionaryCreateMutable, cf, "CFDictionaryCreateMutable")
	purego.RegisterLibFunc(&cfDictionarySetValue, cf, "CFDictionarySetValue")
	purego.RegisterLibFunc(&cfNumberCreate, cf, "CFNumberCreate")
	purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&cfRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&cfRunLoopRunInMode, cf, "CFRunLoopRunInMode")
	purego.RegisterLibFunc(&cfRunLoopStop, cf, "CFRunLoopStop")
	purego.RegisterLibFunc(&cfStringCreateWithBytes, cf, "CFStringCreateWithBytes")

	kCFRunLoopDefaultMode, err = purego.Dlsym(cf, "kCFRunLoopDefaultMode")
	if err != nil {
		panic(err)
	}
	kCFTypeDictionaryKeyCallBacks, err = purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks")
	if err != nil {
		panic(err)
	}
	kCFTypeDictionaryValueCallBacks, err = purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks")
	if err != nil {
		panic(err)
	}

	objc, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	purego.RegisterLibFunc(&objcAutoreleasePoolPush, objc, "objc_autoreleasePoolPush")
	purego.RegisterLibFunc(&objcAutoreleasePoolPop, objc, "objc_autoreleasePoolPop")

	iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	purego.RegisterLibFunc(&ioHIDDeviceGetProperty, iokit, "IOHIDDeviceGetProperty")
	purego.RegisterLibFunc(&ioHIDManagerClose, iokit, "IOHIDManagerClose")
	purego.RegisterLibFunc(&ioHIDManagerCreate, iokit, "IOHIDManagerCreate")
	purego.RegisterLibFunc(&ioHIDManagerOpen, iokit, "IOHIDManagerOpen")
	purego.RegisterLibFunc(&ioHIDManagerSetDeviceMatching, iokit, "IOHIDManagerSetDeviceMatching")
	purego.RegisterLibFunc(&ioHIDManagerRegisterDeviceMatchingCallback, iokit, "IOHIDManagerRegisterDeviceMatchingCallback")
	purego.RegisterLibFunc(&ioHIDManagerScheduleWithRunLoop, iokit, "IOHIDManagerScheduleWithRunLoop")
}

// watcherCtx holds the state passed to the IOKit callback.
// A Go-side reference is kept alive to prevent GC.
type watcherCtx struct {
	ch       chan<- struct{}
	vendorID uint16
}

func deviceMatchingCallback(_ unsafe.Pointer, _ ioReturn, _ uintptr, device ioHIDDeviceRef) {
	if callbackCtx == nil {
		return
	}

	vid, ok := getDeviceVendorID(device)
	if !ok {
		return
	}

	if vid != callbackCtx.vendorID {
		return
	}

	log.Printf("USB device arrived (vendor 0x%04x)", vid)
	select {
	case callbackCtx.ch <- struct{}{}:
	default:
	}
}

// callbackCtx is the package-level reference to the watcher context.
// Kept here so the GC doesn't collect it while the callback is registered.
// Only one watcher is supported at a time.
var callbackCtx *watcherCtx

var deviceMatchingCallbackPtr = purego.NewCallback(deviceMatchingCallback)

func getDeviceVendorID(device ioHIDDeviceRef) (uint16, bool) {
	key := []byte("VendorID")
	skey := cfStringCreateWithBytes(kCFAllocatorDefault, key, cfIndex(len(key)), kCFStringEncodingUTF8, false)
	if skey == 0 {
		return 0, false
	}
	defer cfRelease(cfTypeRef(skey))

	prop := ioHIDDeviceGetProperty(device, skey)
	if prop == 0 {
		return 0, false
	}

	var vid uint16
	if !cfNumberGetValue(cfNumberRef(prop), kCFNumberSInt16Type, unsafe.Pointer(&vid)) {
		return 0, false
	}
	return vid, true
}

// createVendorMatchingDict builds a { "VendorID": vendorID } CFDictionary for
// IOHIDManagerSetDeviceMatching. The manager retains the dictionary, but we
// intentionally leak our reference: this is called once per process and the
// dictionary must outlive the manager anyway.
func createVendorMatchingDict(vendorID uint16) cfDictionaryRef {
	dict := cfDictionaryCreateMutable(kCFAllocatorDefault, 0, kCFTypeDictionaryKeyCallBacks, kCFTypeDictionaryValueCallBacks)

	key := []byte("VendorID")
	skey := cfStringCreateWithBytes(kCFAllocatorDefault, key, cfIndex(len(key)), kCFStringEncodingUTF8, false)

	vid := int32(vendorID)
	num := cfNumberCreate(kCFAllocatorDefault, kCFNumberSInt32Type, unsafe.Pointer(&vid))

	cfDictionarySetValue(dict, uintptr(skey), uintptr(num))
	cfRelease(cfTypeRef(skey))
	cfRelease(cfTypeRef(num))

	return dict
}

// Watch returns a channel that receives a signal each time a USB HID device
// with the given vendor ID appears on the bus. Uses IOKit's device matching
// callback for zero-CPU-cost waiting. The watcher stops when ctx is cancelled.
func Watch(ctx context.Context, vendorID uint16) <-chan struct{} {
	ch := make(chan struct{}, 1)

	wctx := &watcherCtx{
		ch:       ch,
		vendorID: vendorID,
	}
	callbackCtx = wctx

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		mgr := ioHIDManagerCreate(kCFAllocatorDefault, kIOHIDOptionsTypeNone)
		if rv := ioHIDManagerOpen(mgr, kIOHIDOptionsTypeNone); rv != kIOReturnSuccess {
			log.Printf("usbwatch: failed to open IOHIDManager: 0x%08x", rv)
			return
		}

		// Match only our vendor ID at the IOKit level. Matching all HID
		// devices (dict = 0) makes the manager instantiate wrapper objects
		// for every HID device on the system on every arrival, which
		// accumulate in this thread's autorelease pool.
		ioHIDManagerSetDeviceMatching(mgr, createVendorMatchingDict(vendorID))

		rl := cfRunLoopGetCurrent()
		mode := **(**cfStringRef)(unsafe.Pointer(&kCFRunLoopDefaultMode))
		ioHIDManagerScheduleWithRunLoop(mgr, rl, mode)
		ioHIDManagerRegisterDeviceMatchingCallback(mgr, deviceMatchingCallbackPtr, nil)

		// Stop the run loop when the context is cancelled.
		go func() {
			<-ctx.Done()
			cfRunLoopStop(rl)
		}()

		log.Println("usbwatch: listening for USB HID device arrivals")

		// Run the loop in bounded slices, draining the ObjC autorelease pool
		// between them. IOHIDLib autoreleases objects on every callback, and
		// a bare CFRunLoopRun on a Go-created thread has no pool in place, so
		// those objects would otherwise accumulate for the life of the thread.
		for {
			pool := objcAutoreleasePoolPush()
			rv := cfRunLoopRunInMode(mode, 60, false)
			objcAutoreleasePoolPop(pool)
			if rv == kCFRunLoopRunFinished || rv == kCFRunLoopRunStopped {
				break
			}
		}

		ioHIDManagerClose(mgr, kIOHIDOptionsTypeNone)
		cfRelease(cfTypeRef(mgr))
		callbackCtx = nil
		log.Println("usbwatch: stopped")
	}()

	return ch
}
