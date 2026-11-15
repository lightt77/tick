package tray

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// An unbundled binary has no Info.plist to say LSUIElement, so ask for the
// accessory policy directly: menu bar item only, no Dock icon.
static void tickAccessory(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
	});
}
*/
import "C"

func accessory() { C.tickAccessory() }
