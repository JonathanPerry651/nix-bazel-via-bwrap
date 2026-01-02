package main

// KnownMirrors Populated from nixpkgs/pkgs/build-support/fetchurl/mirrors.nix
var KnownMirrors = map[string][]string{
	"gnu": {
		"https://ftpmirror.gnu.org/",
		"https://ftp.nluug.nl/pub/gnu/",
		"https://mirrors.kernel.org/gnu/",
		"https://mirror.ibcp.fr/pub/gnu/",
		"https://mirror.dogado.de/gnu/",
		"https://mirror.tochlab.net/pub/gnu/",
		"https://ftp.gnu.org/pub/gnu/",
		"ftp://ftp.funet.fi/pub/mirrors/ftp.gnu.org/gnu/",
	},
	"savannah": {
		"https://mirror.easyname.at/nongnu/",
		"https://savannah.c3sl.ufpr.br/",
		"https://mirror.csclub.uwaterloo.ca/nongnu/",
		"https://mirror.cedia.org.ec/nongnu/",
		"https://ftp.igh.cnrs.fr/pub/nongnu/",
		"https://mirror6.layerjet.com/nongnu",
		"https://mirror.netcologne.de/savannah/",
		"https://ftp.cc.uoc.gr/mirrors/nongnu.org/",
		"https://nongnu.uib.no/",
		"https://ftp.acc.umu.se/mirror/gnu.org/savannah/",
		"http://mirror2.klaus-uwe.me/nongnu/",
		"http://mirrors.fe.up.pt/pub/nongnu/",
		"http://ftp.twaren.net/Unix/NonGNU/",
		"http://savannah-nongnu-org.ip-connect.vn.ua/",
		"http://www.mirrorservice.org/sites/download.savannah.gnu.org/releases/",
		"http://gnu.mirrors.pair.com/savannah/savannah/",
	},
	"kernel": {
		"https://cdn.kernel.org/pub/",
		"http://linux-kernel.uio.no/pub/",
		"ftp://ftp.funet.fi/pub/mirrors/ftp.kernel.org/pub/",
	},
}

// Derivation represents the JSON structure from `nix show-derivation`
type Derivation struct {
	Outputs map[string]struct {
		Path string `json:"path"`
	} `json:"outputs"`
	InputDrvs interface{}       `json:"inputDrvs"`
	InputSrcs []string          `json:"inputSrcs"`
	Platform  string            `json:"system"`
	Builder   string            `json:"builder"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
}

// Graph is the full derivation graph keying derivation path to Derivation object
type Graph map[string]Derivation

// HttpFileDef represents a fetched file that should be represented as an http_file
type HttpFileDef struct {
	Name       string
	URLs       []string
	Sha256     string
	Path       string // for Executable/DownloadedFilePath logic if needed
	Executable bool
}
