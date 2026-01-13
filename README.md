# SpaceHogs

SpaceHogs is a command-line utility written in Go that scans a directory to find "space hogs"—the files and directories that consume the most disk space. It's designed to be fast and efficient, using concurrency to traverse the filesystem.

## Features

*   Scans a given directory path.
*   Identifies files and directories larger than a specified size.
*   Displays results in a human-readable format.
*   Sorts results to show the largest items first.
*   Allows exclusion of common system directories (e.g., `proc`, `dev`).

## Usage

### Build

To build the tool, you can use the `go build` command:

```sh
go build spacehogs.go
```

### Run

Run the executable with a target directory and a minimum size threshold.

```sh
./spacehogs <directory> <min_size>
```

**`<min_size>` format:**
The size is a number followed by a unit (B, K, M, G, T). For example: `100M`, `2.5G`.

### Examples

**Find all files and directories larger than 500MB in your home directory:**
```sh
./spacehogs ~ 500M
```

**Find items larger than 1GB in `/var/log`, excluding the `dev` directory:**
```sh
./spacehogs --exclude=dev /var/log 1G
```

## License

This project is licensed under the **MIT License**. See the [LICENSE](LICENSE) file for details.
