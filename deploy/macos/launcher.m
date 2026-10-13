// Give the existing Go service a macOS application identity, then replace this
// short-lived launcher in the same process. Business code stays in the Go binary.
#import <Foundation/Foundation.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

int main(int argc, char **argv) {
    @autoreleasepool {
        NSBundle *bundle = [NSBundle mainBundle];
        NSString *root = [bundle objectForInfoDictionaryKey:@"TWCProjectRoot"];
        NSString *server = [bundle pathForResource:@"twc-approval-go-bridge" ofType:nil];
        if (!root || !server || chdir(root.fileSystemRepresentation) != 0) {
            fprintf(stderr, "TWC launcher: project directory or server is unavailable\n");
            return 1;
        }
        if (!getenv("CONFIG_FILE")) {
            NSString *config = [root stringByAppendingPathComponent:@"configs/config.toml"];
            if (setenv("CONFIG_FILE", config.fileSystemRepresentation, 1) != 0) {
                perror("TWC launcher: set CONFIG_FILE");
                return 1;
            }
        }
        char **arguments = calloc((size_t)(argc > 1 ? argc + 1 : 3), sizeof(char *));
        if (!arguments) {
            perror("TWC launcher: allocate arguments");
            return 1;
        }
        arguments[0] = (char *)server.fileSystemRepresentation;
        if (argc == 1) {
            arguments[1] = "server";
        } else {
            for (int i = 1; i < argc; i++) arguments[i] = argv[i];
        }
        execv(arguments[0], arguments);
        perror("TWC launcher: exec server");
        free(arguments);
        return 1;
    }
}
