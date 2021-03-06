package prefab

import (
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sync"
	"time"
)

func download(uri string) (string, error) {
	f, err := ioutil.TempFile("", "download")

	if err != nil {
		return "", err
	}

	defer f.Close()

	resp, err := http.Get(uri)

	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	_, err = io.Copy(f, resp.Body)

	if err != nil {
		return "", err
	}

	return f.Name(), nil
}

type Manifest struct {
	SourceLists     []SourceList             `json:"source_lists"`
	Packages        []Package                `json:"apt_packages"`
	Directories     []Directory              `json:"directories"`
	Templates       []Template               `json:"templates"`
	PackageArchives []PersonalPackageArchive `json:"personal_package_archives"`
	Tarballs        []Tarball                `json:"tarballs"`
	Users           []User                   `json:"users"`
	Symlinks        []Symlink                `json:"symlinks"`
	Services        []Service                `json:"services"`
	Databases       []Database               `json:"postgres_databases"`
	DatabaseUsers   []DatabaseUser           `json:"postgres_database_users"`
	RubyBundles     []RubyBundle             `json:"ruby_bundles"`
}

func Analyze() (Manifest, error) {
	path := "/etc/apt/sources.list.d"

	_, err := ioutil.ReadDir(path)

	if err != nil {
		return Manifest{}, err
	}

	return Manifest{}, nil
}

func (m *Manifest) FixPaths(manifestPath string) {
	for i, template := range m.Templates {
		m.Templates[i].Source = filepath.Join(filepath.Dir(manifestPath), template.Source)
	}

}

func (m Manifest) Begin() error {
	err := os.MkdirAll("/var/prefab", 0777)

	if err != nil {
		return err
	}

	info, err := os.Stat("/var/prefab/apt-update")

	if os.IsNotExist(err) {
		_, err = os.Create("/var/prefab/apt-update")

		if err != nil {
			return err
		}

		log.Println("Run `apt-get update`")
		out, err := exec.Command("apt-get", "update").CombinedOutput()

		if err != nil {
			log.Println(string(out))
			return err
		}

		return nil
	}

	// If the ModTime on this file is older than a week, rerun
	if info.ModTime().Before(time.Now().AddDate(0, 0, -7)) {

		log.Println("Run `apt-get update`")
		out, err := exec.Command("apt-get", "update").CombinedOutput()
		os.Chtimes("/var/prefab/apt-update", time.Now(), time.Now())

		if err != nil {
			log.Println(string(out))
			return err
		}

	}

	return nil

}

func (m Manifest) Converge() error {
	for _, user := range m.Users {
		err := user.Create()

		if err != nil {
			return err
		}
	}

	err := m.Begin()

	if err != nil {
		return err
	}

	apt_update_needed := false

	for _, slist := range m.SourceLists {
		created, err := slist.Install()

		if err != nil {
			return err
		}

		if created {
			apt_update_needed = true
		}
	}

	// If there are Personal Package Archives to install,
	// make sure that the `add-apt-repository` command is available
	if len(m.PackageArchives) > 0 {
		pkg := Package{Name: "python-software-properties"}
		err := pkg.CheckInstall()

		if err != nil {
			return err
		}
	}

	for _, ppa := range m.PackageArchives {
		created, err := ppa.Install()

		if err != nil {
			return err
		}

		if created {
			apt_update_needed = true
		}
	}

	// Replace this with notifications eventually
	if apt_update_needed {
		log.Println("Run `apt-get update`")
		out, err := exec.Command("apt-get", "update").CombinedOutput()

		if err != nil {
			log.Println(string(out))
			return err
		}
	}

	packagesToInstall := []Package{}

	for _, pack := range m.Packages {
		// Find all urls to download
		log.Println("Install package:", pack.QualifiedName())

		if !pack.Installed() {
			packagesToInstall = append(packagesToInstall, pack)
		}
	}

	if len(packagesToInstall) > 0 {

		archiveChannel := make(chan string)

		var wg sync.WaitGroup

		for i := 0; i < 20; i++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				for {

					rawUrl, ok := <-archiveChannel

					if !ok || len(rawUrl) == 0 {
						//Channel is closed, we're finished
						return
					}

					uri, err := url.Parse(rawUrl)

					destination := filepath.Join("/var/cache/apt/archives", path.Base(uri.Path))

					_, err = os.Stat(destination)

					if !os.IsNotExist(err) {
						// Already exists, ignore
						continue
					}

					out, err := os.Create(destination)

					if err != nil {
						continue
					}

					defer out.Close()

					resp, err := http.Get(uri.String())

					if err != nil {
						continue
					}

					defer resp.Body.Close()

					_, err = io.Copy(out, resp.Body)

					if err != nil {
						continue
					}

				}

			}()
		}

		for _, pack := range packagesToInstall {
			// Find all urls to download
			err := pack.ArchiveUrls(archiveChannel)

			if err != nil {
				log.Fatal(err)
			}
		}

		close(archiveChannel)

		wg.Wait()

		for _, pack := range packagesToInstall {
			// Find all urls to download
			err := pack.Install()

			if err != nil {
				log.Fatal(err)
			}
		}
	}

	for _, tarball := range m.Tarballs {
		err := tarball.Unpack()

		if err != nil {
			return err
		}
	}

	for _, dir := range m.Directories {
		err := dir.Create()

		if err != nil {
			return err
		}
	}

	for _, tmpl := range m.Templates {
		err := tmpl.Create()

		if err != nil {
			return err
		}
	}

	for _, symlink := range m.Symlinks {
		err := symlink.Create()

		if err != nil {
			return err
		}
	}

	for _, db := range m.Databases {
		err := db.Create()

		if err != nil {
			return err
		}
	}

	for _, dbu := range m.DatabaseUsers {
		err := dbu.Create()

		if err != nil {
			return err
		}
	}

	for _, rb := range m.RubyBundles {
		err := rb.Install()

		if err != nil {
			return err
		}
	}

	for _, service := range m.Services {
		err := service.Create()

		if err != nil {
			return err
		}
	}

	return nil
}

func (m *Manifest) Add(other Manifest) {
	m.SourceLists = append(m.SourceLists, other.SourceLists...)
	m.Packages = append(m.Packages, other.Packages...)
	m.Directories = append(m.Directories, other.Directories...)
	m.Templates = append(m.Templates, other.Templates...)
	m.PackageArchives = append(m.PackageArchives, other.PackageArchives...)
	m.Tarballs = append(m.Tarballs, other.Tarballs...)
	m.Users = append(m.Users, other.Users...)
	m.Services = append(m.Services, other.Services...)
	m.Databases = append(m.Databases, other.Databases...)
	m.DatabaseUsers = append(m.DatabaseUsers, other.DatabaseUsers...)
	m.Symlinks = append(m.Symlinks, other.Symlinks...)
	m.RubyBundles = append(m.RubyBundles, other.RubyBundles...)
}
