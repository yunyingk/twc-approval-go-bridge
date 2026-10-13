// Inspect the application's actual PID after its launcher execs the Go server.
#import <AppKit/AppKit.h>

int main(int argc, char **argv) {
    @autoreleasepool {
        if (argc != 2) return 2;
        NSString *identifier = [NSString stringWithUTF8String:argv[1]];
        NSMutableArray *applications = [NSMutableArray array];
        for (NSRunningApplication *app in
             [NSRunningApplication runningApplicationsWithBundleIdentifier:identifier]) {
            if (app.terminated) continue;
            [applications addObject:@{
                @"pid": @(app.processIdentifier),
                @"name": app.localizedName ?: @"",
                @"bundle_id": app.bundleIdentifier ?: @"",
                @"bundle_path": app.bundleURL.path ?: @"",
                @"executable_path": app.executableURL.path ?: @"",
                @"icon_present": @(app.icon != nil)
            }];
        }
        NSData *json = [NSJSONSerialization dataWithJSONObject:applications options:0 error:nil];
        if (!json) return 1;
        fwrite(json.bytes, 1, json.length, stdout);
        fputc('\n', stdout);
        return 0;
    }
}
