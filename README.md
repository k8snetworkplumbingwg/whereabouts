# whereabouts

A CNI IPAM plugin that assigns IP addresses cluster-wide

## Usage

[... more to come ...]

## Building

Run the build command from the `./hack` directory:

```
./hack/build-go.sh
```

## Development notes

Run glide with:

```
glide install --strip-vcs --strip-vendor
```

(Otherwise, you might run into issues with nested vendored packages)

## Acknowledgements

Thanks big time to [Tomofumi Hayashi](https://github.com/s1061123), I utilized his [static CNI IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) as a basis for this project to give me a head start!
