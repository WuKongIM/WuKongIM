# repo.py
# DNF Repository objects.
#
# Copyright (C) 2013-2016 Red Hat, Inc.
#
# This copyrighted material is made available to anyone wishing to use,
# modify, copy, or redistribute it subject to the terms and conditions of
# the GNU General Public License v.2, or (at your option) any later version.
# This program is distributed in the hope that it will be useful, but WITHOUT
# ANY WARRANTY expressed or implied, including the implied warranties of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU General
# Public License for more details.  You should have received a copy of the
# GNU General Public License along with this program; if not, write to the
# Free Software Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA
# 02110-1301, USA.  Any Red Hat trademarks that are incorporated in the
# source code or documentation are not subject to the GNU General Public
# License and may only be used or replicated with the express permission of
# Red Hat, Inc.
#

from __future__ import absolute_import
from __future__ import unicode_literals

from dnf.i18n import ucd, _

import dnf.callback
import dnf.conf
import dnf.conf.substitutions
import dnf.const
import dnf.crypto
import dnf.exceptions
import dnf.logging
import dnf.pycomp
import dnf.util
import dnf.yum.misc
import libdnf.error
import libdnf.repo
import functools
import hashlib
import hawkey
import logging
import operator
import os
import re
import shutil
import string
import sys
import time
import traceback

_PACKAGES_RELATIVE_DIR = "packages"
_MIRRORLIST_FILENAME = "mirrorlist"
# Chars allowed in a repo ID
_REPOID_CHARS = string.ascii_letters + string.digits + '-_.:'
# Regex pattern that matches a repo cachedir and captures the repo ID
_CACHEDIR_RE = r'(?P<repoid>[%s]+)\-[%s]{16}' % (re.escape(_REPOID_CHARS),
                                                 string.hexdigits)

# Regex patterns matching any filename that is repo-specific cache data of a
# particular type.  The filename is expected to not contain the base cachedir
# path components.
CACHE_FILES = {
    'metadata': r'^%s\/.*((xml|yaml)(\.gz|\.xz|\.bz2|.zck)?|asc|cachecookie|%s)$' %
                (_CACHEDIR_RE, _MIRRORLIST_FILENAME),
    'packages': r'^%s\/%s\/.+rpm$' % (_CACHEDIR_RE, _PACKAGES_RELATIVE_DIR),
    'dbcache': r'^.+(solv|solvx)$',
}

logger = logging.getLogger("dnf")


def repo_id_invalid(repo_id):
    # :api
    """Return index of an invalid character in the repo ID (if present)."""
    first_invalid = libdnf.repo.Repo.verifyId(repo_id)
    return None if first_invalid < 0 else first_invalid


def _pkg2payload(pkg, progress, *factories):
    for fn in factories:
        pload = fn(pkg, progress)
        if pload is not None:
            return pload
    raise ValueError(_('no matching payload factory for %s') % pkg)


def _download_payloads(payloads, drpm, fail_fast=True):
    # download packages
    def _download_sort_key(payload):
        return not hasattr(payload, 'delta')

    drpm.err.clear()
    targets = [pload._librepo_target()
               for pload in sorted(payloads, key=_download_sort_key)]
    errs = _DownloadErrors()
    try:
        libdnf.repo.PackageTarget.downloadPackages(libdnf.repo.VectorPPackageTarget(targets), fail_fast)
    except RuntimeError as e:
        errs._fatal = str(e)
    drpm.wait()

    # process downloading errors
    errs._recoverable = drpm.err.copy()
    for tgt in targets:
        err = tgt.getErr()
        if err is None or err.startswith('Not finished'):
            continue
        callbacks = tgt.getCallbacks()
        payload = callbacks.package_pload
        pkg = payload.pkg
        if err == 'Already downloaded':
            errs._skipped.add(pkg)
            continue
        pkg.repo._repo.expire()
        errs._pkg_irrecoverable[pkg] = [err]

    return errs


def _update_saving(saving, payloads, errs):
    real, full = saving
    for pload in payloads:
        pkg = pload.pkg
        if pkg in errs:
            real += pload.download_size
            continue
        real += pload.download_size
        full += pload._full_size
    return real, full


class _DownloadErrors(object):
    def __init__(self):
        self._pkg_irrecoverable = {}
        self._val_recoverable = {}
        self._fatal = None
        self._skipped = set()

    def _irrecoverable(self):
        if self._pkg_irrecoverable:
            return self._pkg_irrecoverable
        if self._fatal:
            return {'': [self._fatal]}
        return {}

    @property
    def _recoverable(self):
        return self._val_recoverable

    @_recoverable.setter
    def _recoverable(self, new_dct):
        self._val_recoverable = new_dct

    def _bandwidth_used(self, pload):
        if pload.pkg in self._skipped:
            return 0
        return pload.download_size


class _DetailedLibrepoError(Exception):
    def __init__(self, librepo_err, source_url):
        Exception.__init__(self)
        self.librepo_code = librepo_err.args[0]
        self.librepo_msg = librepo_err.args[1]
        self.source_url = source_url


class _NullKeyImport(dnf.callback.KeyImport):
    def _confirm(self, id, userid, fingerprint, url, timestamp):
        return True


class Metadata(object):
    def __init__(self, repo):
        self._repo = repo

    @property
    def fresh(self):
        # :api
        return self._repo.fresh()


class PackageTargetCallbacks(libdnf.repo.PackageTargetCB):
    def __init__(self, package_pload):
        super(PackageTargetCallbacks, self).__init__()
        self.package_pload = package_pload

    def end(self, status, msg):
        self.package_pload._end_cb(None, status, msg)
        return 0

    def progress(self, totalToDownload, downloaded):
        self.package_pload._progress_cb(None, totalToDownload, downloaded)
        return 0

    def mirrorFailure(self, msg, url):
        self.package_pload._mirrorfail_cb(None, msg, url)
        return 0


class PackagePayload(dnf.callback.Payload):
    def __init__(self, pkg, progress):
        super(PackagePayload, self).__init__(progress)
        self.callbacks = PackageTargetCallbacks(self)
        self.pkg = pkg

    def _end_cb(self, cbdata, lr_status, msg):
        """End callback to librepo operation."""
        status = dnf.callback.STATUS_FAILED
        if msg is None:
            status = dnf.callback.STATUS_OK
        elif msg.startswith('Not finished'):
            return
        elif lr_status == libdnf.repo.PackageTargetCB.TransferStatus_ALREADYEXISTS:
            status = dnf.callback.STATUS_ALREADY_EXISTS

        self.progress.end(self, status, msg)

    def _mirrorfail_cb(self, cbdata, err, url):
        self.progress.end(self, dnf.callback.STATUS_MIRROR, err)

    def _progress_cb(self, cbdata, total, done):
        try:
            self.progress.progress(self, done)
        except Exception:
            exc_type, exc_value, exc_traceback = sys.exc_info()
            except_list = traceback.format_exception(exc_type, exc_value, exc_traceback)
            logger.critical(''.join(except_list))

    @property
    def _full_size(self):
        return self.download_size

    def _librepo_target(self):
        pkg = self.pkg
        pkgdir = pkg.pkgdir
        dnf.util.ensure_dir(pkgdir)

        target_dct = {
            'dest': pkgdir,
            'resume': True,
            'cbdata': self,
            'progresscb': self._progress_cb,
            'endcb': self._end_cb,
            'mirrorfailurecb': self._mirrorfail_cb,
        }
        target_dct.update(self._target_params())

        return libdnf.repo.PackageTarget(
            pkg.repo._repo,
            target_dct['relative_url'],
            target_dct['dest'], target_dct['checksum_type'], target_dct['checksum'],
            target_dct['expectedsize'], target_dct['base_url'], target_dct['resume'],
            0, 0, self.callbacks)


class RPMPayload(PackagePayload):

    def __str__(self):
        return os.path.basename(self.pkg.location)

    def _target_params(self):
        pkg = self.pkg
        ctype, csum = pkg.returnIdSum()
        ctype_code = libdnf.repo.PackageTarget.checksumType(ctype)
        if ctype_code == libdnf.repo.PackageTarget.ChecksumType_UNKNOWN:
            logger.warning(_("unsupported checksum type: %s"), ctype)

        return {
            'relative_url': pkg.location,
            'checksum_type': ctype_code,
            'checksum': csum,
            'expectedsize': pkg.downloadsize,
            'base_url': pkg.baseurl,
        }

    @property
    def download_size(self):
        """Total size of the download."""
        return self.pkg.downloadsize


class RemoteRPMPayload(PackagePayload):

    def __init__(self, remote_location, conf, progress):
        super(RemoteRPMPayload, self).__init__("unused_object", progress)
        self.remote_location = remote_location
        self.remote_size = 0
        self.conf = conf
        s = (self.conf.releasever or "") + self.conf.substitutions.get('basearch')
        digest = hashlib.sha256(s.encode('utf8')).hexdigest()[:16]
        repodir = "commandline-" + digest
        self.pkgdir = os.path.join(self.conf.cachedir, repodir, "packages")
        dnf.util.ensure_dir(self.pkgdir)
        self.local_path = os.path.join(self.pkgdir, self.__str__().lstrip("/"))

    def __str__(self):
        return os.path.basename(self.remote_location)

    def _progress_cb(self, cbdata, total, done):
        self.remote_size = total
        try:
            self.progress.progress(self, done)
        except Exception:
            exc_type, exc_value, exc_traceback = sys.exc_info()
            except_list = traceback.format_exception(exc_type, exc_value, exc_traceback)
            logger.critical(''.join(except_list))

    def _librepo_target(self):
        return libdnf.repo.PackageTarget(
            self.conf._config, os.path.basename(self.remote_location),
            self.pkgdir, 0, None, 0, os.path.dirname(self.remote_location),
            True, 0, 0, self.callbacks)

    @property
    def download_size(self):
        """Total size of the download."""
        return self.remote_size


class MDPayload(dnf.callback.Payload):

    def __init__(self, progress):
        super(MDPayload, self).__init__(progress)
        self._text = ""
        self._download_size = 0
        self.fastest_mirror_running = False
        self.mirror_failures = set()

    def __str__(self):
        if dnf.pycomp.PY3:
            return self._text
        else:
            return self._text.encode('utf-8')

    def __unicode__(self):
        return self._text

    def _progress_cb(self, cbdata, total, done):
        self._download_size = total
        self.progress.progress(self, done)

    def _fastestmirror_cb(self, cbdata, stage, data):
        if stage == libdnf.repo.RepoCB.FastestMirrorStage_DETECTION:
            # pinging mirrors, this might take a while
            msg = _('determining the fastest mirror (%s hosts).. ') % data
            self.fastest_mirror_running = True
        elif stage == libdnf.repo.RepoCB.FastestMirrorStage_STATUS and self.fastest_mirror_running:
            # done.. report but ignore any errors
            msg = 'error: %s\n' % data if data else 'done.\n'
        else:
            return
        self.progress.message(msg)

    def _mirror_failure_cb(self, cbdata, msg, url, metadata):
        self.mirror_failures.add(msg)
        msg = 'error: %s (%s).' % (msg, url)
        logger.debug(msg)

    @property
    def download_size(self):
        return self._download_size

    @property
    def progress(self):
        return self._progress

    @progress.setter
    def progress(self, progress):
        if progress is None:
            progress = dnf.callback.NullDownloadProgress()
        self._progress = progress

    def start(self, text):
        self._text = text
        self.progress.start(1, 0)

    def end(self):
        self._download_size = 0
        self.progress.end(self, None, None)


# use the local cache even if it's expired. download if there's no cache.
SYNC_LAZY = libdnf.repo.Repo.SyncStrategy_LAZY
# use the local cache, even if it's expired, never download.
SYNC_ONLY_CACHE = libdnf.repo.Repo.SyncStrategy_ONLY_CACHE
# try the cache, if it is expired download new md.
SYNC_TRY_CACHE = libdnf.repo.Repo.SyncStrategy_TRY_CACHE


class RepoCallbacks(libdnf.repo.RepoCB):
    def __init__(self, repo):
        super(RepoCallbacks, self).__init__()
        self._repo = repo
        self._md_pload = repo._md_pload

    def start(self, what):
        self._md_pload.start(what)

    def end(self):
        self._md_pload.end()

    def progress(self, totalToDownload, downloaded):
        self._md_pload._progress_cb(None, totalToDownload, downloaded)
        return 0

    def fastestMirror(self, stage, ptr):
        self._md_pload._fastestmirror_cb(None, stage, ptr)

    def handleMirrorFailure(self, msg, url, metadata):
        self._md_pload._mirror_failure_cb(None, msg, url, metadata)
        return 0

    def repokeyImport(self, id, userid, fingerprint, url, timestamp):
        return self._repo._key_import._confirm(id, userid, fingerprint, url, timestamp)


class Repo(dnf.conf.RepoConf):
    # :api
    DEFAULT_SYNC = SYNC_TRY_CACHE

    def __init__(self, name=None, parent_conf=None):
        # :api
        super(Repo, self).__init__(section=name, parent=parent_conf)

        self._config.this.disown()  # _repo will be the owner of _config
        self._repo = libdnf.repo.Repo(name if name else "", self._config)

        self._md_pload = MDPayload(dnf.callback.NullDownloadProgress())
        self._callbacks = RepoCallbacks(self)
        self._callbacks.this.disown()  # _repo will be the owner of callbacks
        self._repo.setCallbacks(self._callbacks)

        self._pkgdir = None
        self._key_import = _NullKeyImport()
        self.metadata = None  # :api
        self._repo.setSyncStrategy(SYNC_ONLY_CACHE if parent_conf and parent_conf.cacheonly else self.DEFAULT_SYNC)
        if parent_conf:
            self._repo.setSubstitutions(parent_conf.substitutions)
        self._substitutions = dnf.conf.substitutions.Substitutions()
        self._check_config_file_age = parent_conf.check_config_file_age \
            if parent_conf is not None else True

    @property
    def id(self):
        # :api
        return self._repo.getId()

    @property
    def repofile(self):
        # :api
        return self._repo.getRepoFilePath()

    @repofile.setter
    def repofile(self, value):
        self._repo.setRepoFilePath(value)

    @property
    def pkgdir(self):
        # :api
        if self._repo.isLocal():
            return self._repo.getLocalBaseurl()
        return self.cache_pkgdir()

    def cache_pkgdir(self):
        if self._pkgdir is not None:
            return self._pkgdir
        return os.path.join(self._repo.getCachedir(), _PACKAGES_RELATIVE_DIR)

    @pkgdir.setter
    def pkgdir(self, val):
        # :api
        self._pkgdir = val

    @property
    def _pubring_dir(self):
        return os.path.join(self._repo.getCachedir(), 'pubring')

    @property
    def load_metadata_other(self):
        return self._repo.getLoadMetadataOther()

    @load_metadata_other.setter
    def load_metadata_other(self, val):
        self._repo.setLoadMetadataOther(val)

    def __lt__(self, other):
        return self.id < other.id

    def __repr__(self):
        return "<%s %s>" % (self.__class__.__name__, self.id)

    def __setattr__(self, name, value):
        super(Repo, self).__setattr__(name, value)

    def disable(self):
        # :api
        self._repo.disable()

    def enable(self):
        # :api
        self._repo.enable()

    def add_metadata_type_to_download(self, metadata_type):
        # :api
        """Ask for additional repository metadata type to download.

        Given metadata_type is appended to the default metadata set when
        repository is downloaded.

        Parameters
        ----------
        metadata_type: string

        Example: add_metadata_type_to_download("productid")
        """
        self._repo.addMetadataTypeToDownload(metadata_type)

    def remove_metadata_type_from_download(self, metadata_type):
        # :api
        """Stop asking for this additional repository metadata type
        in download.

        Given metadata_type is no longer downloaded by default
        when this repository is downloaded.

        Parameters
        ----------
        metadata_type: string

        Example: remove_metadata_type_from_download("productid")
        """
        self._repo.removeMetadataTypeFromDownload(metadata_type)

    def get_metadata_path(self, metadata_type):
        # :api
        """Return path to the file with downloaded repository metadata of given type.

        Parameters
        ----------
        metadata_type: string
        """
        return self._repo.getMetadataPath(metadata_type)

    def get_metadata_content(self, metadata_type):
        # :api
        """Return content of the file with downloaded repository metadata of given type.

        Content of compressed metadata file is returned uncompressed.

        Parameters
        ----------
        metadata_type: string
        """
        return self._repo.getMetadataContent(metadata_type)

    def load(self):
        # :api
        """Load the metadata for this repo.

        Depending on the configuration and the age and consistence of data
        available on the disk cache, either loads the metadata from the cache or
        downloads them from the mirror, baseurl or metalink.

        This method will by default not try to refresh already loaded data if
        called repeatedly.

        Returns True if this call to load() caused a fresh metadata download.

        """
        ret = False
        try:
            ret = self._repo.load()
        except (libdnf.error.Error, RuntimeError) as e:
            if self._md_pload.mirror_failures:
                msg = "Errors during downloading metadata for repository '%s':" % self.id
                for failure in self._md_pload.mirror_failures:
                    msg += "\n  - %s" % failure
                logger.warning(msg)
            raise dnf.exceptions.RepoError(str(e))
        finally:
            self._md_pload.mirror_failures = set()
        self.metadata = Metadata(self._repo)
        return ret

    def _metadata_expire_in(self):
        """Get the number of seconds after which the cached metadata will expire.

        Returns a tuple, boolean whether there even is cached metadata and the
        number of seconds it will expire in. Negative number means the metadata
        has expired already, None that it never expires.

        """
        if not self.metadata:
            self._repo.loadCache(False)
        if self.metadata:
            if self.metadata_expire == -1:
                return True, None
            expiration = self._repo.getExpiresIn()
            if self._repo.isExpired():
                expiration = min(0, expiration)
            return True, expiration
        return False, 0

    def _set_key_import(self, key_import):
        self._key_import = key_import

    def set_progress_bar(self, progress):
        # :api
        self._md_pload.progress = progress

    def get_http_headers(self):
        # :api
        """Returns user defined http headers.

        Returns
        -------
        headers : tuple of strings
        """
        return self._repo.getHttpHeaders()

    def set_http_headers(self, headers):
        # :api
        """Sets http headers.

        Sets new http headers and rewrites existing ones.

        Parameters
        ----------
        headers : tuple or list of strings
            Example: set_http_headers(["User-Agent: Agent007", "MyFieldName: MyFieldValue"])
        """
        self._repo.setHttpHeaders(headers)

    def remote_location(self, location, schemes=('http', 'ftp', 'file', 'https')):
        """
        :param location: relative location inside the repo
        :param schemes: list of allowed protocols. Default is ('http', 'ftp', 'file', 'https')
        :return: absolute url (string) or None
        """
        def schemes_filter(url_list):
            for url in url_list:
                if schemes:
                    s = dnf.pycomp.urlparse.urlparse(url)[0]
                    if s in schemes:
                        return os.path.join(url, location.lstrip('/'))
                else:
                    return os.path.join(url, location.lstrip('/'))
            return None

        if not location:
            return None

        mirrors = self._repo.getMirrors()
        if mirrors:
            return schemes_filter(mirrors)
        elif self.baseurl:
            return schemes_filter(self.baseurl)
