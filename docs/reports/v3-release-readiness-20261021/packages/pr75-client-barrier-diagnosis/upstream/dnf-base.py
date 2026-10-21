# Copyright 2005 Duke University
# Copyright (C) 2012-2018 Red Hat, Inc.
#
# This program is free software; you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation; either version 2 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Library General Public License for more details.
#
# You should have received a copy of the GNU General Public License
# along with this program; if not, write to the Free Software
# Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.

"""
Supplies the Base class.
"""

from __future__ import absolute_import
from __future__ import division
from __future__ import print_function
from __future__ import unicode_literals

import argparse
import dnf
import libdnf.transaction

from copy import deepcopy
from dnf.comps import CompsQuery
from dnf.i18n import _, P_, ucd
from dnf.util import _parse_specs
from dnf.db.history import SwdbInterface
from dnf.yum import misc
try:
    from collections.abc import Sequence
except ImportError:
    from collections import Sequence
import datetime
import dnf.callback
import dnf.comps
import dnf.conf
import dnf.conf.read
import dnf.crypto
import dnf.dnssec
import dnf.drpm
import dnf.exceptions
import dnf.goal
import dnf.history
import dnf.lock
import dnf.logging
# WITH_MODULES is used by ansible (lib/ansible/modules/packaging/os/dnf.py)
try:
    import dnf.module.module_base
    WITH_MODULES = True
except ImportError:
    WITH_MODULES = False
import dnf.persistor
import dnf.plugin
import dnf.query
import dnf.repo
import dnf.repodict
import dnf.rpm.connection
import dnf.rpm.miscutils
import dnf.rpm.transaction
import dnf.sack
import dnf.selector
import dnf.subject
import dnf.transaction
import dnf.util
import dnf.yum.rpmtrans
import functools
import gc
import hawkey
import itertools
import logging
import math
import os
import operator
import re
import rpm
import tempfile
import time
import shutil


logger = logging.getLogger("dnf")


class Base(object):

    def __init__(self, conf=None):
        # :api
        self._closed = False
        self._conf = conf or self._setup_default_conf()
        self._goal = None
        self._repo_persistor = None
        self._sack = None
        self._transaction = None
        self._priv_ts = None
        self._comps = None
        self._comps_trans = dnf.comps.TransactionBunch()
        self._history = None
        self._tempfiles = set()
        self._trans_tempfiles = set()
        self._ds_callback = dnf.callback.Depsolve()
        self._logging = dnf.logging.Logging()
        self._repos = dnf.repodict.RepoDict()
        self._rpm_probfilter = set([rpm.RPMPROB_FILTER_OLDPACKAGE])
        self._plugins = dnf.plugin.Plugins()
        self._trans_success = False
        self._trans_install_set = False
        self._tempfile_persistor = None
        #  self._update_security_filters is used by ansible
        self._update_security_filters = []
        self._update_security_options = {}
        self._allow_erasing = False
        self._repo_set_imported_gpg_keys = set()
        self.output = None

    def __enter__(self):
        return self

    def __exit__(self, *exc_args):
        self.close()

    def __del__(self):
        self.close()

    def _add_tempfiles(self, files):
        if self._transaction:
            self._trans_tempfiles.update(files)
        elif self.conf.destdir:
            pass
        else:
            self._tempfiles.update(files)

    def _add_repo_to_sack(self, repo):
        repo.load()
        mdload_flags = dict(load_filelists=True,
                            load_presto=repo.deltarpm,
                            load_updateinfo=True)
        if repo.load_metadata_other:
            mdload_flags["load_other"] = True
        try:
            self._sack.load_repo(repo._repo, build_cache=True, **mdload_flags)
        except hawkey.Exception as e:
            logger.debug(_("loading repo '{}' failure: {}").format(repo.id, e))
            raise dnf.exceptions.RepoError(
                _("Loading repository '{}' has failed").format(repo.id))

    @staticmethod
    def _setup_default_conf():
        conf = dnf.conf.Conf()
        subst = conf.substitutions
        if 'releasever' not in subst:
            subst['releasever'] = \
                dnf.rpm.detect_releasever(conf.installroot)
        return conf

    def _setup_modular_excludes(self):
        hot_fix_repos = [i.id for i in self.repos.iter_enabled() if i.module_hotfixes]
        try:
            solver_errors = self.sack.filter_modules(
                self._moduleContainer, hot_fix_repos, self.conf.installroot,
                self.conf.module_platform_id, update_only=False, debugsolver=self.conf.debug_solver,
                module_obsoletes=self.conf.module_obsoletes)
        except hawkey.Exception as e:
            raise dnf.exceptions.Error(ucd(e))
        if solver_errors:
            logger.warning(
                dnf.module.module_base.format_modular_solver_errors(solver_errors[0]))

    def _setup_excludes_includes(self, only_main=False):
        disabled = set(self.conf.disable_excludes)
        if 'all' in disabled and WITH_MODULES:
            self._setup_modular_excludes()
            return
        repo_includes = []
        repo_excludes = []
        # first evaluate repo specific includes/excludes
        if not only_main:
            for r in self.repos.iter_enabled():
                if r.id in disabled:
                    continue
                if len(r.includepkgs) > 0:
                    incl_query = self.sack.query().filterm(empty=True)
                    for incl in set(r.includepkgs):
                        subj = dnf.subject.Subject(incl)
                        incl_query = incl_query.union(subj.get_best_query(
                            self.sack, with_nevra=True, with_provides=False, with_filenames=False))
                    incl_query.filterm(reponame=r.id)
                    repo_includes.append((incl_query.apply(), r.id))
                excl_query = self.sack.query().filterm(empty=True)
                for excl in set(r.excludepkgs):
                    subj = dnf.subject.Subject(excl)
                    excl_query = excl_query.union(subj.get_best_query(
                        self.sack, with_nevra=True, with_provides=False, with_filenames=False))
                excl_query.filterm(reponame=r.id)
                if excl_query:
                    repo_excludes.append((excl_query, r.id))

        # then main (global) includes/excludes because they can mask
        # repo specific settings
        if 'main' not in disabled:
            include_query = self.sack.query().filterm(empty=True)
            if len(self.conf.includepkgs) > 0:
                for incl in set(self.conf.includepkgs):
                    subj = dnf.subject.Subject(incl)
                    include_query = include_query.union(subj.get_best_query(
                        self.sack, with_nevra=True, with_provides=False, with_filenames=False))
            exclude_query = self.sack.query().filterm(empty=True)
            for excl in set(self.conf.excludepkgs):
                subj = dnf.subject.Subject(excl)
                exclude_query = exclude_query.union(subj.get_best_query(
                    self.sack, with_nevra=True, with_provides=False, with_filenames=False))
            if len(self.conf.includepkgs) > 0:
                self.sack.add_includes(include_query)
                self.sack.set_use_includes(True)
            if exclude_query:
                self.sack.add_excludes(exclude_query)

        if repo_includes:
            for query, repoid in repo_includes:
                self.sack.add_includes(query)
                self.sack.set_use_includes(True, repoid)

        if repo_excludes:
            for query, repoid in repo_excludes:
                self.sack.add_excludes(query)

        if not only_main and WITH_MODULES:
            self._setup_modular_excludes()

    def _store_persistent_data(self):
        if self._repo_persistor and not self.conf.cacheonly:
            expired = [r.id for r in self.repos.iter_enabled()
                       if (r.metadata and r._repo.isExpired())]
            self._repo_persistor.expired_to_add.update(expired)
            self._repo_persistor.save()

        if self._tempfile_persistor:
            self._tempfile_persistor.save()

    @property
    def comps(self):
        # :api
        if self._comps is None:
            self.read_comps(arch_filter=True)
        return self._comps

    @property
    def conf(self):
        # :api
        return self._conf

    @property
    def repos(self):
        # :api
        return self._repos

    @repos.deleter
    def repos(self):
        # :api
        self._repos = None

    @property
    @dnf.util.lazyattr("_priv_rpmconn")
    def _rpmconn(self):
        return dnf.rpm.connection.RpmConnection(self.conf.installroot)

    @property
    def sack(self):
        # :api
        return self._sack

    @property
    def _moduleContainer(self):
        if self.sack is None:
            raise dnf.exceptions.Error("Sack was not initialized")
        if self.sack._moduleContainer is None:
            self.sack._moduleContainer = libdnf.module.ModulePackageContainer(
                False, self.conf.installroot, self.conf.substitutions["arch"], self.conf.persistdir)
        return self.sack._moduleContainer

    @property
    def transaction(self):
        # :api
        return self._transaction

    @transaction.setter
    def transaction(self, value):
        # :api
        if self._transaction:
            raise ValueError('transaction already set')
        self._transaction = value

    def _activate_persistor(self):
        self._repo_persistor = dnf.persistor.RepoPersistor(self.conf.cachedir)

    def init_plugins(self, disabled_glob=(), enable_plugins=(), cli=None):
        # :api
        """Load plugins and run their __init__()."""
        if self.conf.plugins:
            self._plugins._load(self.conf, disabled_glob, enable_plugins)
        self._plugins._run_init(self, cli)

    def pre_configure_plugins(self):
        # :api
        """Run plugins pre_configure() method."""
        self._plugins._run_pre_config()

    def configure_plugins(self):
        # :api
        """Run plugins configure() method."""
        self._plugins._run_config()

    def unload_plugins(self):
        # :api
        """Run plugins unload() method."""
        self._plugins._unload()

    def update_cache(self, timer=False):
        # :api

        period = self.conf.metadata_timer_sync
        if self._repo_persistor is None:
            self._activate_persistor()
        persistor = self._repo_persistor
        if timer:
            if dnf.util.on_metered_connection():
                msg = _('Metadata timer caching disabled '
                        'when running on metered connection.')
                logger.info(msg)
                return False
            if dnf.util.on_ac_power() is False:
                msg = _('Metadata timer caching disabled '
                        'when running on a battery.')
                logger.info(msg)
                return False
            if period <= 0:
                msg = _('Metadata timer caching disabled.')
                logger.info(msg)
                return False
            since_last_makecache = persistor.since_last_makecache()
            if since_last_makecache is not None and since_last_makecache < period:
                logger.info(_('Metadata cache refreshed recently.'))
                return False
            for repo in self.repos.values():
                repo._repo.setMaxMirrorTries(1)

        if not self.repos._any_enabled():
            logger.info(_('There are no enabled repositories in "{}".').format(
                '", "'.join(self.conf.reposdir)))
            return False

        for r in self.repos.iter_enabled():
            (is_cache, expires_in) = r._metadata_expire_in()
            if expires_in is None:
                logger.info(_('%s: will never be expired and will not be refreshed.'), r.id)
            elif not is_cache or expires_in <= 0:
                logger.debug(_('%s: has expired and will be refreshed.'), r.id)
                r._repo.expire()
            elif timer and expires_in < period:
                # expires within the checking period:
                msg = _("%s: metadata will expire after %d seconds and will be refreshed now")
                logger.debug(msg, r.id, expires_in)
                r._repo.expire()
            else:
                logger.debug(_('%s: will expire after %d seconds.'), r.id,
                             expires_in)

        if timer:
            persistor.reset_last_makecache = True
        self.fill_sack(load_system_repo=False, load_available_repos=True)  # performs the md sync
        logger.info(_('Metadata cache created.'))
        return True

    def fill_sack(self, load_system_repo=True, load_available_repos=True):
        # :api
        """Prepare the Sack and the Goal objects. """
        timer = dnf.logging.Timer('sack setup')
        self.reset(sack=True, goal=True)
        self._sack = dnf.sack._build_sack(self)
        lock = dnf.lock.build_metadata_lock(self.conf.cachedir, self.conf.exit_on_lock)
        with lock:
            if load_system_repo is not False:
                try:
                    # FIXME: If build_cache=True, @System.solv is incorrectly updated in install-
                    # remove loops
                    self._sack.load_system_repo(build_cache=False)
                except IOError:
                    if load_system_repo != 'auto':
                        raise
            if load_available_repos:
                error_repos = []
                mts = 0
                age = time.time()
                # Iterate over installed GPG keys and check their validity using DNSSEC
                if self.conf.gpgkey_dns_verification:
                    dnf.dnssec.RpmImportedKeys.check_imported_keys_validity()
                for r in self.repos.iter_enabled():
                    try:
                        self._add_repo_to_sack(r)
                        if r._repo.getTimestamp() > mts:
                            mts = r._repo.getTimestamp()
                        if r._repo.getAge() < age:
                            age = r._repo.getAge()
                        logger.debug(_("%s: using metadata from %s."), r.id,
                                     dnf.util.normalize_time(
                                         r._repo.getMaxTimestamp()))
                    except dnf.exceptions.RepoError as e:
                        r._repo.expire()
                        if r.skip_if_unavailable is False:
                            raise
                        logger.warning("Error: %s", e)
                        error_repos.append(r.id)
                        r.disable()
                if error_repos:
                    logger.warning(
                        _("Ignoring repositories: %s"), ', '.join(error_repos))
                if self.repos._any_enabled():
                    if age != 0 and mts != 0:
                        logger.info(_("Last metadata expiration check: %s ago on %s."),
                                    datetime.timedelta(seconds=int(age)),
                                    dnf.util.normalize_time(mts))
            else:
                self.repos.all().disable()
        conf = self.conf
        self._sack._configure(conf.installonlypkgs, conf.installonly_limit, conf.allow_vendor_change)
        self._setup_excludes_includes()
        timer()
        self._goal = dnf.goal.Goal(self._sack)
        self._goal.protect_running_kernel = conf.protect_running_kernel
        self._plugins.run_sack()
        return self._sack

    def fill_sack_from_repos_in_cache(self, load_system_repo=True):
        # :api
        """
        Prepare Sack and Goal objects and also load all enabled repositories from cache only,
        it doesn't download anything and it doesn't check if metadata are expired.
        If there is not enough metadata present (repond.xml or both primary.xml and solv file
        are missing) given repo is either skipped or it throws a RepoError exception depending
        on skip_if_unavailable configuration.
        """
        timer = dnf.logging.Timer('sack setup')
        self.reset(sack=True, goal=True)
        self._sack = dnf.sack._build_sack(self)
        lock = dnf.lock.build_metadata_lock(self.conf.cachedir, self.conf.exit_on_lock)
        with lock:
            if load_system_repo is not False:
                try:
                    # FIXME: If build_cache=True, @System.solv is incorrectly updated in install-
                    # remove loops
                    self._sack.load_system_repo(build_cache=False)
                except IOError:
                    if load_system_repo != 'auto':
                        raise

            error_repos = []
            # Iterate over installed GPG keys and check their validity using DNSSEC
            if self.conf.gpgkey_dns_verification:
                dnf.dnssec.RpmImportedKeys.check_imported_keys_validity()
            for repo in self.repos.iter_enabled():
                try:
                    repo._repo.loadCache(throwExcept=True, ignoreMissing=True)
                    mdload_flags = dict(load_filelists=True,
                                        load_presto=repo.deltarpm,
                                        load_updateinfo=True)
                    if repo.load_metadata_other:
                        mdload_flags["load_other"] = True

                    self._sack.load_repo(repo._repo, **mdload_flags)

                    logger.debug(_("%s: using metadata from %s."), repo.id,
                                 dnf.util.normalize_time(
                                     repo._repo.getMaxTimestamp()))
                except (RuntimeError, hawkey.Exception) as e:
                    if repo.skip_if_unavailable is False:
                        raise dnf.exceptions.RepoError(
                            _("loading repo '{}' failure: {}").format(repo.id, e))
                    else:
                        logger.debug(_("loading repo '{}' failure: {}").format(repo.id, e))
                    error_repos.append(repo.id)
                    repo.disable()
            if error_repos:
                logger.warning(
                    _("Ignoring repositories: %s"), ', '.join(error_repos))

        conf = self.conf
        self._sack._configure(conf.installonlypkgs, conf.installonly_limit, conf.allow_vendor_change)
        self._setup_excludes_includes()
        timer()
        self._goal = dnf.goal.Goal(self._sack)
        self._goal.protect_running_kernel = conf.protect_running_kernel
        self._plugins.run_sack()
        return self._sack

    def _finalize_base(self):
        self._tempfile_persistor = dnf.persistor.TempfilePersistor(
            self.conf.cachedir)

        if not self.conf.keepcache:
            self._clean_packages(self._tempfiles)
            if self._trans_success:
                self._trans_tempfiles.update(
                    self._tempfile_persistor.get_saved_tempfiles())
                self._tempfile_persistor.empty()
                if self._trans_install_set:
                    self._clean_packages(self._trans_tempfiles)
            else:
                self._tempfile_persistor.tempfiles_to_add.update(
                    self._trans_tempfiles)

        if self._tempfile_persistor.tempfiles_to_add:
            logger.info(_("The downloaded packages were saved in cache "
                          "until the next successful transaction."))
            logger.info(_("You can remove cached packages by executing "
                          "'%s'."), "{prog} clean packages".format(prog=dnf.util.MAIN_PROG))

        # Do not trigger the lazy creation:
        if self._history is not None:
            self.history.close()
        self._store_persistent_data()
        self._closeRpmDB()
        self._trans_success = False

    def close(self):
        # :api
        """Close all potential handles and clean cache.

        Typically the handles are to data sources and sinks.

        """

        if self._closed:
            return
        logger.log(dnf.logging.DDEBUG, 'Cleaning up.')
        self._closed = True
        self._finalize_base()
        self.reset(sack=True, repos=True, goal=True)
        self._plugins = None

    def read_all_repos(self, opts=None):
        # :api
        """Read repositories from the main conf file and from .repo files."""

        reader = dnf.conf.read.RepoReader(self.conf, opts)
        for repo in reader:
            try:
                self.repos.add(repo)
            except dnf.exceptions.ConfigError as e:
                logger.warning(e)

    def reset(self, sack=False, repos=False, goal=False):
        # :api
        """Make the Base object forget about various things."""
        if sack:
            self._sack = None
        if repos:
            self._repos = dnf.repodict.RepoDict()
        if goal:
            self._goal = None
            if self._sack is not None:
                self._goal = dnf.goal.Goal(self._sack)
                self._goal.protect_running_kernel = self.conf.protect_running_kernel
            if self._sack and self._moduleContainer:
                # sack must be set to enable operations on moduleContainer
                self._moduleContainer.rollback()
            if self._history is not None:
                self.history.close()
            self._comps_trans = dnf.comps.TransactionBunch()
            self._transaction = None
        self._update_security_filters = []
        if sack and goal:
            # We've just done this, above:
            #
            #      _sack                     _goal
            #         |                        |
            #    -- [CUT] --              -- [CUT] --
            #         |                        |
            #         v                |       v
            #    +----------------+   [C]  +-------------+
            #    | DnfSack object | <-[U]- | Goal object |
            #    +----------------+   [T]  +-------------+
            #      |^    |^    |^      |
            #      ||    ||    ||
            #      ||    ||    ||         |
            #   +--||----||----||---+    [C]
            #   |  v|    v|    v|   | <--[U]-- _transaction
            #   | Pkg1  Pkg2  PkgN  |    [T]
            #   |                   |     |
            #   | Transaction oject |
            #   +-------------------+
            #
            # At this point, the DnfSack object would be released only
            # eventually, by Python's generational garbage collector, due to the
            # cyclic references DnfSack<->Pkg1 ... DnfSack<->PkgN.
            #
            # The delayed release is a problem: the DnfSack object may
            # (indirectly) own "page file" file descriptors in libsolv, via
            # libdnf. For example,
            #
            #   sack->priv->pool->repos[1]->repodata[1]->store.pagefd = 7
            #   sack->priv->pool->repos[1]->repodata[2]->store.pagefd = 8
            #
            # These file descriptors are closed when the DnfSack object is
            # eventually released, that is, when dnf_sack_finalize() (in libdnf)
            # calls pool_free() (in libsolv).
            #
            # We need that to happen right now, as callers may want to unmount
            # the filesystems which those file descriptors refer to immediately
            # after reset() returns. Therefore, force a garbage collection here.
            gc.collect()

    def _closeRpmDB(self):
        """Closes down the instances of rpmdb that could be open."""
        del self._ts

    _TS_FLAGS_TO_RPM = {'noscripts': rpm.RPMTRANS_FLAG_NOSCRIPTS,
                        'notriggers': rpm.RPMTRANS_FLAG_NOTRIGGERS,
                        'nodocs': rpm.RPMTRANS_FLAG_NODOCS,
                        'test': rpm.RPMTRANS_FLAG_TEST,
                        'justdb': rpm.RPMTRANS_FLAG_JUSTDB,
                        'nocontexts': rpm.RPMTRANS_FLAG_NOCONTEXTS,
                        'nocrypto': rpm.RPMTRANS_FLAG_NOFILEDIGEST}
    if hasattr(rpm, 'RPMTRANS_FLAG_NOCAPS'):
        # Introduced in rpm-4.14
        _TS_FLAGS_TO_RPM['nocaps'] = rpm.RPMTRANS_FLAG_NOCAPS

    _TS_VSFLAGS_TO_RPM = {'nocrypto': rpm._RPMVSF_NOSIGNATURES |
                          rpm._RPMVSF_NODIGESTS}

    @property
    def goal(self):
        return self._goal

    @property
    def _ts(self):
        """Set up the RPM transaction set that will be used
           for all the work."""
        if self._priv_ts is not None:
            return self._priv_ts
        self._priv_ts = dnf.rpm.transaction.TransactionWrapper(
            self.conf.installroot)
        self._priv_ts.setFlags(0)  # reset everything.
        for flag in self.conf.tsflags:
            rpm_flag = self._TS_FLAGS_TO_RPM.get(flag)
            if rpm_flag is None:
                logger.critical(_('Invalid tsflag in config file: %s'), flag)
                continue
            self._priv_ts.addTsFlag(rpm_flag)
            vs_flag = self._TS_VSFLAGS_TO_RPM.get(flag)
            if vs_flag is not None:
                self._priv_ts.pushVSFlags(vs_flag)

        if not self.conf.diskspacecheck:
            self._rpm_probfilter.add(rpm.RPMPROB_FILTER_DISKSPACE)

        if self.conf.ignorearch:
            self._rpm_probfilter.add(rpm.RPMPROB_FILTER_IGNOREARCH)

        probfilter = functools.reduce(operator.or_, self._rpm_probfilter, 0)
        self._priv_ts.setProbFilter(probfilter)
        return self._priv_ts

    @_ts.deleter
    def _ts(self):
        """Releases the RPM transaction set. """
        if self._priv_ts is None:
            return
        self._priv_ts.close()
        del self._priv_ts
        self._priv_ts = None

    def read_comps(self, arch_filter=False):
        # :api
        """Create the groups object to access the comps metadata."""
        timer = dnf.logging.Timer('loading comps')
        self._comps = dnf.comps.Comps()

        logger.log(dnf.logging.DDEBUG, 'Getting group metadata')
        for repo in self.repos.iter_enabled():
            if not repo.enablegroups:
                continue
            if not repo.metadata:
                continue
            comps_fn = repo._repo.getCompsFn()
            if not comps_fn:
                continue

            logger.log(dnf.logging.DDEBUG,
                       'Adding group file from repository: %s', repo.id)
            gen_dir = os.path.join(os.path.dirname(comps_fn), 'gen')
            gen_file = os.path.join(gen_dir, 'groups.xml')
            temp_file = None
            try:
                if not os.path.exists(gen_dir):
                    os.makedirs(gen_dir, mode=0o755)
                misc.decompress(comps_fn, dest=gen_file, check_timestamps=True)
            except (PermissionError, dnf.exceptions.MiscError):
                temp_file = tempfile.NamedTemporaryFile()
                gen_file = temp_file.name
                misc.decompress(comps_fn, dest=gen_file, check_timestamps=False)
            try:
                self._comps._add_from_xml_filename(gen_file)
            except dnf.exceptions.CompsError as e:
                msg = _('Failed to add groups file for repository: %s - %s')
                logger.critical(msg, repo.id, e)
            if temp_file:
                temp_file.close()

        if arch_filter:
            self._comps._i.arch_filter(
                [self._conf.substitutions['basearch']])
        timer()
        return self._comps

    def _getHistory(self):
        """auto create the history object that to access/append the transaction
           history information. """
        if self._history is None:
            releasever = self.conf.releasever
            self._history = SwdbInterface(self.conf.persistdir, releasever=releasever)
        return self._history

    history = property(fget=lambda self: self._getHistory(),
                       fset=lambda self, value: setattr(
                           self, "_history", value),
                       fdel=lambda self: setattr(self, "_history", None),
                       doc="DNF SWDB Interface Object")

    def _goal2transaction(self, goal):
        ts = self.history.rpm
        all_obsoleted = set(goal.list_obsoleted())
        installonly_query = self._get_installonly_query()
        installonly_query.apply()
        installonly_query_installed = installonly_query.installed().apply()

        for pkg in goal.list_downgrades():
            obs = goal.obsoleted_by_package(pkg)
            downgraded = obs[0]
            self._ds_callback.pkg_added(downgraded, 'dd')
            self._ds_callback.pkg_added(pkg, 'd')
            ts.add_downgrade(pkg, downgraded, obs[1:])
        for pkg in goal.list_reinstalls():
            self._ds_callback.pkg_added(pkg, 'r')
            obs = goal.obsoleted_by_package(pkg)
            nevra_pkg = str(pkg)
            # reinstall could obsolete multiple packages with the same NEVRA or different NEVRA
            # Set the package with the same NEVRA as reinstalled
            obsoletes = []
            for obs_pkg in obs:
                if str(obs_pkg) == nevra_pkg:
                    obsoletes.insert(0, obs_pkg)
                else:
                    obsoletes.append(obs_pkg)
            reinstalled = obsoletes[0]
            ts.add_reinstall(pkg, reinstalled, obsoletes[1:])
        for pkg in goal.list_installs():
            self._ds_callback.pkg_added(pkg, 'i')
            obs = goal.obsoleted_by_package(pkg)
            # Skip obsoleted packages that are not part of all_obsoleted,
            # they are handled as upgrades/downgrades.
            # Also keep RPMs with the same name - they're not always in all_obsoleted.
            obs = [i for i in obs if i in all_obsoleted or i.name == pkg.name]

            reason = goal.get_reason(pkg)

            #  Inherit reason if package is installonly an package with same name is installed
            #  Use the same logic like upgrade
            #  Upgrade of installonly packages result in install or install and remove step
            if pkg in installonly_query and installonly_query_installed.filter(name=pkg.name):
                reason = ts.get_reason(pkg)

            # inherit the best reason from obsoleted packages
            for obsolete in obs:
                reason_obsolete = ts.get_reason(obsolete)
                if libdnf.transaction.TransactionItemReasonCompare(reason, reason_obsolete) == -1:
                    reason = reason_obsolete

            ts.add_install(pkg, obs, reason)
            cb = lambda pkg: self._ds_callback.pkg_added(pkg, 'od')
            dnf.util.mapall(cb, obs)
        for pkg in goal.list_upgrades():
            obs = goal.obsoleted_by_package(pkg)
            upgraded = None
            for i in obs:
                # try to find a package with matching name as the upgrade
                if i.name == pkg.name:
                    upgraded = i
                    break
            if upgraded is None:
                # no matching name -> pick the first one
                upgraded = obs.pop(0)
            else:
                obs.remove(upgraded)
            # Skip obsoleted packages that are not part of all_obsoleted,
            # they are handled as upgrades/downgrades.
            # Also keep RPMs with the same name - they're not always in all_obsoleted.
            obs = [i for i in obs if i in all_obsoleted or i.name == pkg.name]

            cb = lambda pkg: self._ds_callback.pkg_added(pkg, 'od')
            dnf.util.mapall(cb, obs)
            if pkg in installonly_query:
                ts.add_install(pkg, obs)
            else:
                ts.add_upgrade(pkg, upgraded, obs)
                self._ds_callback.pkg_added(upgraded, 'ud')
            self._ds_callback.pkg_added(pkg, 'u')
        erasures = goal.list_erasures()
        if erasures:
            remaining_installed_query = self.sack.query(flags=hawkey.IGNORE_EXCLUDES).installed()
            remaining_installed_query.filterm(pkg__neq=erasures)
            for pkg in erasures:
                if remaining_installed_query.filter(name=pkg.name):
                    remaining = remaining_installed_query[0]
                    ts.get_reason(remaining)
                    self.history.set_reason(remaining, ts.get_reason(remaining))
                self._ds_callback.pkg_added(pkg, 'e')
                reason = goal.get_reason(pkg)
                ts.add_erase(pkg, reason)
        return ts

    def _query_matches_installed(self, q):
        """ See what packages in the query match packages (also in older
            versions, but always same architecture) that are already installed.

            Unlike in case of _sltr_matches_installed(), it is practical here
            to know even the packages in the original query that can still be
            installed.
        """
        inst = q.installed()
        inst_per_arch = inst._na_dict()
        avail_per_arch = q.available()._na_dict()
        avail_l = []
        inst_l = []
        for na in avail_per_arch:
            if na in inst_per_arch:
                inst_l.append(inst_per_arch[na][0])
            else:
                avail_l.append(avail_per_arch[na])
        return inst_l, avail_l

    def _sltr_matches_installed(self, sltr):
        """ See if sltr matches a patches that is (in older version or different
            architecture perhaps) already installed.
        """
        inst = self.sack.query().installed().filterm(pkg=sltr.matches())
        return list(inst)

    def iter_userinstalled(self):
        """Get iterator over the packages installed by the user."""
        return (pkg for pkg in self.sack.query().installed()
                if self.history.user_installed(pkg))

    def _run_hawkey_goal(self, goal, allow_erasing):
        ret = goal.run(
            allow_uninstall=allow_erasing, force_best=self.conf.best,
            ignore_weak_deps=(not self.conf.install_weak_deps))
        if self.conf.debug_solver:
            goal.write_debugdata('./debugdata/rpms')
        return ret

    def _set_excludes_from_weak_to_goal(self):
        """
        Add exclude_from_weak from configuration and autodetect unmet weak deps exclude them from candidates to satisfy
        weak dependencies
        """
        self._goal.reset_exclude_from_weak()
        if self.conf.exclude_from_weak_autodetect:
            self._goal.exclude_from_weak_autodetect()

        for weak_exclude in self.conf.exclude_from_weak:
            subj = dnf.subject.Subject(weak_exclude)
            query = subj.get_best_query(self.sack, with_nevra=True, with_provides=False, with_filenames=False)
            query = query.available()
            self._goal.add_exclude_from_weak(query)

    def resolve(self, allow_erasing=False):
        # :api
        """Build the transaction set."""
        exc = None
        self._finalize_comps_trans()

        timer = dnf.logging.Timer('depsolve')
        self._ds_callback.start()
        goal = self._goal
        if goal.req_has_erase():
            goal.push_userinstalled(self.sack.query().installed(),
                                    self.history)
        elif not self.conf.upgrade_group_objects_upgrade:
            # exclude packages installed from groups
            # these packages will be marked to installation
            # which could prevent them from upgrade, downgrade
            # to prevent "conflicting job" error it's not applied
            # to "remove" and "reinstall" commands

            solver = self._build_comps_solver()
            solver._exclude_packages_from_installed_groups(self)

        goal.add_protected(self.sack.query().filterm(
            name=self.conf.protected_packages))

        self._set_excludes_from_weak_to_goal()

        if not self._run_hawkey_goal(goal, allow_erasing):
            if self.conf.debuglevel >= 6:
                goal.log_decisions()
            msg = dnf.util._format_resolve_problems(goal.problem_rules())
            exc = dnf.exceptions.DepsolveError(msg)
        else:
            self._transaction = self._goal2transaction(goal)

        self._ds_callback.end()
        timer()

        got_transaction = self._transaction is not None and \
            len(self._transaction) > 0
        if got_transaction:
            msg = self._transaction._rpm_limitations()
            if msg:
                exc = dnf.exceptions.Error(msg)

        if exc is not None:
            raise exc

        self._plugins.run_resolved()

        # auto-enable module streams based on installed RPMs
        new_pkgs = self._goal.list_installs()
        new_pkgs += self._goal.list_upgrades()
        new_pkgs += self._goal.list_downgrades()
        new_pkgs += self._goal.list_reinstalls()
        self.sack.set_modules_enabled_by_pkgset(self._moduleContainer, new_pkgs)

        return got_transaction

    def do_transaction(self, display=()):
        # :api
        if not isinstance(display, Sequence):
            display = [display]
        display = \
            [dnf.yum.rpmtrans.LoggingTransactionDisplay()] + list(display)

        if not self.transaction:
            # packages are not changed, but comps and modules changes need to be committed
            self._moduleContainer.save()
            self._moduleContainer.updateFailSafeData()
            if self._history and (self._history.group or self._history.env):
                cmdline = None
                if hasattr(self, 'args') and self.args:
                    cmdline = ' '.join(self.args)
                elif hasattr(self, 'cmds') and self.cmds:
                    cmdline = ' '.join(self.cmds)
                old = self.history.last()
                if old is None:
                    rpmdb_version = self._ts.dbCookie()
                else:
                    rpmdb_version = old.end_rpmdb_version

                self.history.beg(rpmdb_version, [], [], cmdline)
                self.history.end(rpmdb_version)
            self._plugins.run_pre_transaction()
            self._plugins.run_transaction()
            self._trans_success = True
            return

        tid = None
        logger.info(_('Running transaction check'))
        lock = dnf.lock.build_rpmdb_lock(self.conf.persistdir,
                                         self.conf.exit_on_lock)
        with lock:
            self.transaction._populate_rpm_ts(self._ts)

            msgs = self._run_rpm_check()
            if msgs:
                msg = _('Error: transaction check vs depsolve:')
                logger.error(msg)
                for msg in msgs:
                    logger.error(msg)
                raise dnf.exceptions.TransactionCheckError(msg)

            logger.info(_('Transaction check succeeded.'))

            timer = dnf.logging.Timer('transaction test')
            logger.info(_('Running transaction test'))

            self._ts.order()  # order the transaction
            self._ts.clean()  # release memory not needed beyond this point

            testcb = dnf.yum.rpmtrans.RPMTransaction(self, test=True)
            tserrors = self._ts.test(testcb)

            if len(tserrors) > 0:
                for msg in testcb.messages():
                    logger.critical(_('RPM: {}').format(msg))
                errstring = _('Transaction test error:') + '\n'
                for descr in tserrors:
                    errstring += '  %s\n' % ucd(descr)

                summary = self._trans_error_summary(errstring)
                if summary:
                    errstring += '\n' + summary

                raise dnf.exceptions.Error(errstring)
            del testcb

            logger.info(_('Transaction test succeeded.'))
            #  With RPMTRANS_FLAG_TEST return just before anything is stored permanently
            if self._ts.isTsFlagSet(rpm.RPMTRANS_FLAG_TEST):
                return
            timer()

            # save module states on disk right before entering rpm transaction,
            # because we want system in recoverable state if transaction gets interrupted
            self._moduleContainer.save()
            self._moduleContainer.updateFailSafeData()

            # unset the sigquit handler
            timer = dnf.logging.Timer('transaction')
            # setup our rpm ts callback
            cb = dnf.yum.rpmtrans.RPMTransaction(self, displays=display)
            if self.conf.debuglevel < 2:
                for display_ in cb.displays:
                    display_.output = False

            self._plugins.run_pre_transaction()

            logger.info(_('Running transaction'))
            tid = self._run_transaction(cb=cb)
        timer()
        self._plugins.unload_removed_plugins(self.transaction)
        self._plugins.run_transaction()

        # log post transaction summary
        def _pto_callback(action, tsis):
            msgs = []
            for tsi in tsis:
                msgs.append('{}: {}'.format(action, str(tsi)))
            return msgs
        for msg in dnf.util._post_transaction_output(self, self.transaction, _pto_callback):
            logger.debug(msg)

        return tid

    def _trans_error_summary(self, errstring):
        """Parse the error string for 'interesting' errors which can
        be grouped, such as disk space issues.

        :param errstring: the error string
        :return: a string containing a summary of the errors
        """
        summary = ''
        # do disk space report first
        p = re.compile(r'needs (\d+)(K|M)B(?: more space)? on the (\S+) filesystem')
        disk = {}
        for m in p.finditer(errstring):
            size_in_mb = int(m.group(1)) if m.group(2) == 'M' else math.ceil(
                int(m.group(1)) / 1024.0)
            if m.group(3) not in disk:
                disk[m.group(3)] = size_in_mb
            if disk[m.group(3)] < size_in_mb:
                disk[m.group(3)] = size_in_mb

        if disk:
            summary += _('Disk Requirements:') + "\n"
            for k in disk:
                summary += "   " + P_(
                    'At least {0}MB more space needed on the {1} filesystem.',
                    'At least {0}MB more space needed on the {1} filesystem.',
                    disk[k]).format(disk[k], k) + '\n'

        if not summary:
            return None

        summary = _('Error Summary') + '\n-------------\n' + summary

        return summary

    def _record_history(self):
        return self.conf.history_record and \
            not self._ts.isTsFlagSet(rpm.RPMTRANS_FLAG_TEST)

    def _run_transaction(self, cb):
        """
        Perform the RPM transaction.

        :return: history database transaction ID or None
        """

        tid = None
        if self._record_history():
            using_pkgs_pats = list(self.conf.history_record_packages)
            installed_query = self.sack.query().installed()
            using_pkgs = installed_query.filter(name=using_pkgs_pats).run()
            rpmdbv = self._ts.dbCookie()
            lastdbv = self.history.last()
            if lastdbv is not None:
                lastdbv = lastdbv.end_rpmdb_version

            if lastdbv is None or rpmdbv != lastdbv:
                logger.debug(_("RPMDB altered outside of {prog}.").format(
                    prog=dnf.util.MAIN_PROG_UPPER))

            cmdline = None
            if hasattr(self, 'args') and self.args:
                cmdline = ' '.join(self.args)
            elif hasattr(self, 'cmds') and self.cmds:
                cmdline = ' '.join(self.cmds)

            comment = self.conf.comment if self.conf.comment else ""
            tid = self.history.beg(rpmdbv, using_pkgs, [], cmdline, comment)

        if self.conf.reset_nice:
            onice = os.nice(0)
            if onice:
                try:
                    os.nice(-onice)
                except:
                    onice = 0

        logger.log(dnf.logging.DDEBUG, 'RPM transaction start.')
        errors = self._ts.run(cb.callback, '')
        logger.log(dnf.logging.DDEBUG, 'RPM transaction over.')
        # ts.run() exit codes are, hmm, "creative": None means all ok, empty
        # list means some errors happened in the transaction and non-empty
        # list that there were errors preventing the ts from starting...
        if self.conf.reset_nice:
            try:
                os.nice(onice)
            except:
                pass
        dnf.util._sync_rpm_trans_with_swdb(self._ts, self._transaction)

        if errors is None:
            pass
        elif len(errors) == 0:
            # If there is no failing element it means that some "global" error
            # occurred (like rpm failed to obtain the transaction lock). Just pass
            # the rpm logs on to the user and raise an Error.
            # If there are failing elements the problem is related to those
            # elements and the Error is raised later, after saving the failure
            # to the history and printing out the transaction table to user.
            failed = [el for el in self._ts if el.Failed()]
            if not failed:
                for msg in cb.messages():
                    logger.critical(_('RPM: {}').format(msg))
                msg = _('Could not run transaction.')
                raise dnf.exceptions.Error(msg)
        else:
            logger.critical(_("Transaction couldn't start:"))
            for e in errors:
                logger.critical(ucd(e[0]))
            if self._record_history() and not self._ts.isTsFlagSet(rpm.RPMTRANS_FLAG_TEST):
                self.history.end(rpmdbv)
            msg = _("Could not run transaction.")
            raise dnf.exceptions.Error(msg)

        for i in ('ts_all_fn', 'ts_done_fn'):
            if hasattr(cb, i):
                fn = getattr(cb, i)
                try:
                    misc.unlink_f(fn)
                except (IOError, OSError):
                    msg = _('Failed to remove transaction file %s')
                    logger.critical(msg, fn)

        # keep install_set status because _verify_transaction will clean it
        self._trans_install_set = bool(self._transaction.install_set)

        # sync up what just happened versus what is in the rpmdb
        if not self._ts.isTsFlagSet(rpm.RPMTRANS_FLAG_TEST):
            self._verify_transaction(cb.verify_tsi_package)

        return tid

    def _verify_transaction(self, verify_pkg_cb=None):
        transaction_items = [
            tsi for tsi in self.transaction
            if tsi.action != libdnf.transaction.TransactionItemAction_REASON_CHANGE]
        total = len(transaction_items)

        def display_banner(pkg, count):
            count += 1
            if verify_pkg_cb is not None:
                verify_pkg_cb(pkg, count, total)
            return count

        timer = dnf.logging.Timer('verify transaction')
        count = 0

        rpmdb_sack = dnf.sack.rpmdb_sack(self)

        # mark group packages that are installed on the system as installed in the db
        q = rpmdb_sack.query().installed()
        names = set([i.name for i in q])
        for ti in self.history.group:
            g = ti.getCompsGroupItem()
            for p in g.getPackages():
                if p.getName() in names:
                    p.setInstalled(True)
                    p.save()

        # TODO: installed groups in environments

        # Post-transaction verification is no longer needed,
        # because DNF trusts error codes returned by RPM.
        # Verification banner is displayed to preserve UX.
        # TODO: drop in future DNF
        for tsi in transaction_items:
            count = display_banner(tsi.pkg, count)

        rpmdbv = self._ts.dbCookie()
        self.history.end(rpmdbv)

        timer()
        self._trans_success = True

    def _download_remote_payloads(self, payloads, drpm, progress, callback_total, fail_fast=True):
        lock = dnf.lock.build_download_lock(self.conf.cachedir, self.conf.exit_on_lock)
        with lock:
            beg_download = time.time()
            est_remote_size = sum(pload.download_size for pload in payloads)
            total_drpm = len(
                [payload for payload in payloads if isinstance(payload, dnf.drpm.DeltaPayload)])
            # compatibility part for tools that do not accept total_drpms keyword
            if progress.start.__code__.co_argcount == 4:
                progress.start(len(payloads), est_remote_size, total_drpms=total_drpm)
            else:
                progress.start(len(payloads), est_remote_size)
            errors = dnf.repo._download_payloads(payloads, drpm, fail_fast)

            if errors._irrecoverable():
                raise dnf.exceptions.DownloadError(errors._irrecoverable())

            remote_size = sum(errors._bandwidth_used(pload)
                              for pload in payloads)
            saving = dnf.repo._update_saving((0, 0), payloads,
                                             errors._recoverable)

            retries = self.conf.retries
            forever = retries == 0
            while errors._recoverable and (forever or retries > 0):
                if retries > 0:
                    retries -= 1

                msg = _("Some packages were not downloaded. Retrying.")
                logger.info(msg)

                remaining_pkgs = [pkg for pkg in errors._recoverable]
                payloads = \
                    [dnf.repo._pkg2payload(pkg, progress, dnf.repo.RPMPayload)
                     for pkg in remaining_pkgs]
                est_remote_size = sum(pload.download_size
                                      for pload in payloads)
                progress.start(len(payloads), est_remote_size)
                errors = dnf.repo._download_payloads(payloads, drpm, fail_fast)

                if errors._irrecoverable():
                    raise dnf.exceptions.DownloadError(errors._irrecoverable())

                remote_size += \
                    sum(errors._bandwidth_used(pload) for pload in payloads)
                saving = dnf.repo._update_saving(saving, payloads, {})

            if errors._recoverable:
                msg = dnf.exceptions.DownloadError.errmap2str(
                    errors._recoverable)
                logger.info(msg)

        if callback_total is not None:
            callback_total(remote_size, beg_download)

        (real, full) = saving
        if real != full:
            if real < full:
                msg = _("Delta RPMs reduced %.1f MB of updates to %.1f MB "
                        "(%.1f%% saved)")
                percent = 100 - real / full * 100
            elif real > full:
                msg = _("Failed Delta RPMs increased %.1f MB of updates to %.1f MB "
                        "(%.1f%% wasted)")
                percent = 100 - full / real * 100
            logger.info(msg, full / 1024 ** 2, real / 1024 ** 2, percent)

    def download_packages(self, pkglist, progress=None, callback_total=None):
        # :api
        """Download the packages specified by the given list of packages.

        `pkglist` is a list of packages to download, `progress` is an optional
         DownloadProgress instance, `callback_total` an optional callback to
         output messages about the download operation.

        """
        remote_pkgs, local_pkgs = self._select_remote_pkgs(pkglist)
        if remote_pkgs:
            if progress is None:
                progress = dnf.callback.NullDownloadProgress()
            drpm = dnf.drpm.DeltaInfo(self.sack.query().installed(),
                                      progress, self.conf.deltarpm_percentage)
            self._add_tempfiles([pkg.localPkg() for pkg in remote_pkgs])
            payloads = [dnf.repo._pkg2payload(pkg, progress, drpm.delta_factory,
                                              dnf.repo.RPMPayload)
                        for pkg in remote_pkgs]
            self._download_remote_payloads(payloads, drpm, progress, callback_total)

        if self.conf.destdir:
            for pkg in local_pkgs:
                if pkg.baseurl:
                    location = os.path.join(pkg.get_local_baseurl(),
                                            pkg.location.lstrip("/"))
                else:
                    location = os.path.join(pkg.repo.pkgdir, pkg.location.lstrip("/"))
                shutil.copy(location, self.conf.destdir)

    def add_remote_rpms(self, path_list, strict=True, progress=None):
        # :api
        pkgs = []
        if not path_list:
            return pkgs
        if self._goal.req_length():
            raise dnf.exceptions.Error(
                _("Cannot add local packages, because transaction job already exists"))
        pkgs_error = []
        for path in path_list:
            if not os.path.exists(path) and '://' in path:
                # download remote rpm to a tempfile
                path = dnf.util._urlopen_progress(path, self.conf, progress)
                self._add_tempfiles([path])
            try:
                pkgs.append(self.sack.add_cmdline_package(path))
            except IOError as e:
                logger.warning(e)
                pkgs_error.append(path)
        self._setup_excludes_includes(only_main=True)
        if pkgs_error and strict:
            raise IOError(_("Could not open: {}").format(' '.join(pkgs_error)))
        return pkgs

    def _sig_check_pkg(self, po):
        """Verify the GPG signature of the given package object.

        :param po: the package object to verify the signature of
        :return: (result, error_string)
           where result is::

              0 = GPG signature verifies ok or verification is not required.
              1 = GPG verification failed but installation of the right GPG key
                    might help.
              2 = Fatal GPG verification error, give up.
        """
        if po._from_cmdline:
            check = self.conf.localpkg_gpgcheck
            hasgpgkey = 0
        else:
            repo = self.repos[po.repoid]
            check = repo.gpgcheck
            hasgpgkey = not not repo.gpgkey

        if check:
            root = self.conf.installroot
            ts = dnf.rpm.transaction.initReadOnlyTransaction(root)
            sigresult = dnf.rpm.miscutils.checkSig(ts, po.localPkg())
            localfn = os.path.basename(po.localPkg())
            del ts
            if sigresult == 0:
                result = 0
                msg = ''

            elif sigresult == 1:
                if hasgpgkey:
                    result = 1
                else:
                    result = 2
                msg = _('Public key for %s is not installed') % localfn

            elif sigresult == 2:
                result = 2
                msg = _('Problem opening package %s') % localfn

            elif sigresult == 3:
                if hasgpgkey:
                    result = 1
                else:
                    result = 2
                result = 1
                msg = _('Public key for %s is not trusted') % localfn

            elif sigresult == 4:
                result = 2
                msg = _('Package %s is not signed') % localfn

        else:
            result = 0
            msg = ''

        return result, msg

    def package_signature_check(self, pkg):
        # :api
        """Verify the GPG signature of the given package object.

        :param pkg: the package object to verify the signature of
        :return: (result, error_string)
           where result is::

              0 = GPG signature verifies ok or verification is not required.
              1 = GPG verification failed but installation of the right GPG key
                    might help.
              2 = Fatal GPG verification error, give up.
        """
        return self._sig_check_pkg(pkg)

    def _clean_packages(self, packages):
        for fn in packages:
            if not os.path.exists(fn):
                continue
            try:
                misc.unlink_f(fn)
            except OSError:
                logger.warning(_('Cannot remove %s'), fn)
                continue
            else:
                logger.log(dnf.logging.DDEBUG,
                           _('%s removed'), fn)

    def _do_package_lists(self, pkgnarrow='all', patterns=None, showdups=None,
                       ignore_case=False, reponame=None):
        """Return a :class:`misc.GenericHolder` containing
        lists of package objects.  The contents of the lists are
        specified in various ways by the arguments.

        :param pkgnarrow: a string specifying which types of packages
           lists to produces, such as updates, installed, available,
           etc.
        :param patterns: a list of names or wildcards specifying
           packages to list
        :param showdups: whether to include duplicate packages in the
           lists
        :param ignore_case: whether to ignore case when searching by
           package names
        :param reponame: limit packages list to the given repository
        :return: a :class:`misc.GenericHolder` instance with the
           following lists defined::

             available = list of packageObjects
             installed = list of packageObjects
             upgrades = tuples of packageObjects (updating, installed)
             extras = list of packageObjects
             obsoletes = tuples of packageObjects (obsoleting, installed)
             recent = list of packageObjects
        """
        if showdups is None:
            showdups = self.conf.showdupesfromrepos
        if patterns is None:
            return self._list_pattern(
                pkgnarrow, patterns, showdups, ignore_case, reponame)

        assert not dnf.util.is_string_type(patterns)
        list_fn = functools.partial(
            self._list_pattern, pkgnarrow, showdups=showdups,
            ignore_case=ignore_case, reponame=reponame)
        if patterns is None or len(patterns) == 0:
            return list_fn(None)
        yghs = map(list_fn, patterns)
        return functools.reduce(lambda a, b: a.merge_lists(b), yghs)

    def _list_pattern(self, pkgnarrow, pattern, showdups, ignore_case,
                      reponame=None):
        def is_from_repo(package):
            """Test whether given package originates from the repository."""
            if reponame is None:
                return True
            return self.history.repo(package) == reponame

        def pkgs_from_repo(packages):
            """Filter out the packages which do not originate from the repo."""
            return (package for package in packages if is_from_repo(package))

        def query_for_repo(query):
            """Filter out the packages which do not originate from the repo."""
            if reponame is None:
                return query
            return query.filter(reponame=reponame)

        ygh = misc.GenericHolder(iter=pkgnarrow)

        installed = []
        available = []
        reinstall_available = []
        old_available = []
        updates = []
        obsoletes = []
        obsoletesTuples = []
        recent = []
        extras = []
        autoremove = []

        # do the initial pre-selection
        ic = ignore_case
        q = self.sack.query()
        if pattern is not None:
            subj = dnf.subject.Subject(pattern, ignore_case=ic)
            q = subj.get_best_query(self.sack, with_provides=False)

        # list all packages - those installed and available:
        if pkgnarrow == 'all':
            dinst = {}
            ndinst = {}  # Newest versions by name.arch
            for po in q.installed():
                dinst[po.pkgtup] = po
                if showdups:
                    continue
                key = (po.name, po.arch)
                if key not in ndinst or po > ndinst[key]:
                    ndinst[key] = po
            installed = list(pkgs_from_repo(dinst.values()))

            avail = query_for_repo(q.available())
            if not showdups:
                avail = avail.filterm(latest_per_arch_by_priority=True)
            for pkg in avail:
                if showdups:
                    if pkg.pkgtup in dinst:
                        reinstall_available.append(pkg)
                    else:
                        available.append(pkg)
                else:
                    key = (pkg.name, pkg.arch)
                    if pkg.pkgtup in dinst:
                        reinstall_available.append(pkg)
                    elif key not in ndinst or pkg.evr_gt(ndinst[key]):
                        available.append(pkg)
                    else:
                        old_available.append(pkg)

        # produce the updates list of tuples
        elif pkgnarrow == 'upgrades':
            updates = query_for_repo(q).filterm(upgrades_by_priority=True)
            # reduce a query to security upgrades if they are specified
            updates = self._merge_update_filters(updates, upgrade=True)
            # reduce a query to latest packages
            updates = updates.latest().run()

        # installed only
        elif pkgnarrow == 'installed':
            installed = list(pkgs_from_repo(q.installed()))

        # available in a repository
        elif pkgnarrow == 'available':
            if showdups:
                avail = query_for_repo(q).available()
                installed_dict = q.installed()._na_dict()
                for avail_pkg in avail:
                    key = (avail_pkg.name, avail_pkg.arch)
                    installed_pkgs = installed_dict.get(key, [])
                    same_ver = [pkg for pkg in installed_pkgs
                                if pkg.evr == avail_pkg.evr]
                    if len(same_ver) > 0:
                        reinstall_available.append(avail_pkg)
                    else:
                        available.append(avail_pkg)
            else:
                # we will only look at the latest versions of packages:
                available_dict = query_for_repo(
                    q).available().filterm(latest_per_arch_by_priority=True)._na_dict()
                installed_dict = q.installed().latest()._na_dict()
                for (name, arch) in available_dict:
                    avail_pkg = available_dict[(name, arch)][0]
                    inst_pkg = installed_dict.get((name, arch), [None])[0]
                    if not inst_pkg or avail_pkg.evr_gt(inst_pkg):
                        available.append(avail_pkg)
                    elif avail_pkg.evr_eq(inst_pkg):
                        reinstall_available.append(avail_pkg)
                    else:
                        old_available.append(avail_pkg)

        # packages to be removed by autoremove
        elif pkgnarrow == 'autoremove':
            autoremove_q = query_for_repo(q)._unneeded(self.history.swdb)
            autoremove = autoremove_q.run()

        # not in a repo but installed
        elif pkgnarrow == 'extras':
            extras = [pkg for pkg in q.extras() if is_from_repo(pkg)]

        # obsoleting packages (and what they obsolete)
        elif pkgnarrow == 'obsoletes':
            inst = q.installed()
            obsoletes = query_for_repo(
                self.sack.query()).filter(obsoletes_by_priority=inst)
            # reduce a query to security upgrades if they are specified
            obsoletes = self._merge_update_filters(obsoletes, warning=False, upgrade=True)
            obsoletesTuples = []
            for new in obsoletes:
                obsoleted_reldeps = new.obsoletes
                obsoletesTuples.extend(
                    [(new, old) for old in
                     inst.filter(provides=obsoleted_reldeps)])

        # packages recently added to the repositories
        elif pkgnarrow == 'recent':
            avail = q.available()
            if not showdups:
                avail = avail.filterm(latest_per_arch_by_priority=True)
            recent = query_for_repo(avail)._recent(self.conf.recent)

        ygh.installed = installed
        ygh.available = available
        ygh.reinstall_available = reinstall_available
        ygh.old_available = old_available
        ygh.updates = updates
        ygh.obsoletes = obsoletes
        ygh.obsoletesTuples = obsoletesTuples
        ygh.recent = recent
        ygh.extras = extras
        ygh.autoremove = autoremove

        return ygh

    def _add_comps_trans(self, trans):
        self._comps_trans += trans
        return len(trans)

    def _remove_if_unneeded(self, query):
        """
        Mark to remove packages that are not required by any user installed package (reason group
        or user)
        :param query: dnf.query.Query() object
        """
        query = query.installed()
        if not query:
            return

        unneeded_pkgs = query._safe_to_remove(self.history.swdb, debug_solver=False)
        unneeded_pkgs_history = query.filter(
            pkg=[i for i in query if self.history.group.is_removable_pkg(i.name)])
        pkg_with_dependent_pkgs = unneeded_pkgs_history.difference(unneeded_pkgs)

        # mark packages with dependent packages as a dependency to allow removal with dependent
        # package
        for pkg in pkg_with_dependent_pkgs:
            self.history.set_reason(pkg, libdnf.transaction.TransactionItemReason_DEPENDENCY)
        unneeded_pkgs = unneeded_pkgs.intersection(unneeded_pkgs_history)

        remove_packages = query.intersection(unneeded_pkgs)
        if remove_packages:
            for pkg in remove_packages:
                self._goal.erase(pkg, clean_deps=self.conf.clean_requirements_on_remove)

    def _finalize_comps_trans(self):
        trans = self._comps_trans
        basearch = self.conf.substitutions['basearch']

        def trans_upgrade(query, remove_query, comps_pkg):
            sltr = dnf.selector.Selector(self.sack)
            sltr.set(pkg=query)
            self._goal.upgrade(select=sltr)
            return remove_query

        def trans_install(query, remove_query, comps_pkg, strict):
            if self.conf.multilib_policy == "all":
                if not comps_pkg.requires:
                    self._install_multiarch(query, strict=strict)
                else:
                    # it installs only one arch for conditional packages
                    installed_query = query.installed().apply()
                    self._report_already_installed(installed_query)
                    sltr = dnf.selector.Selector(self.sack)
                    sltr.set(provides="({} if {})".format(comps_pkg.name, comps_pkg.requires))
                    self._goal.install(select=sltr, optional=not strict)

            else:
                sltr = dnf.selector.Selector(self.sack)
                if comps_pkg.requires:
                    sltr.set(provides="({} if {})".format(comps_pkg.name, comps_pkg.requires))
                else:
                    if self.conf.obsoletes:
                        query = query.union(self.sack.query().filterm(obsoletes=query))
                    sltr.set(pkg=query)
                self._goal.install(select=sltr, optional=not strict)
            return remove_query

        def trans_remove(query, remove_query, comps_pkg):
            remove_query = remove_query.union(query)
            return remove_query

        remove_query = self.sack.query().filterm(empty=True)
        attr_fn = ((trans.install, functools.partial(trans_install, strict=True)),
                   (trans.install_opt, functools.partial(trans_install, strict=False)),
                   (trans.upgrade, trans_upgrade),
                   (trans.remove, trans_remove))

        for (attr, fn) in attr_fn:
            for comps_pkg in attr:
                query_args = {'name': comps_pkg.name}
                if (comps_pkg.basearchonly):
                    query_args.update({'arch': basearch})
                q = self.sack.query().filterm(**query_args).apply()
                q.filterm(arch__neq=["src", "nosrc"])
                if not q:
                    package_string = comps_pkg.name
                    if comps_pkg.basearchonly:
                        package_string += '.' + basearch
                    logger.warning(_('No match for group package "{}"').format(package_string))
                    continue
                remove_query = fn(q, remove_query, comps_pkg)
                self._goal.group_members.add(comps_pkg.name)

        self._remove_if_unneeded(remove_query)

    def _build_comps_solver(self):
        def reason_fn(pkgname):
            q = self.sack.query().installed().filterm(name=pkgname)
            if not q:
                return None
            try:
                return self.history.rpm.get_reason(q[0])
            except AttributeError:
                return libdnf.transaction.TransactionItemReason_UNKNOWN

        return dnf.comps.Solver(self.history, self._comps, reason_fn)

    def environment_install(self, env_id, types, exclude=None, strict=True, exclude_groups=None):
        # :api
        """Installs packages of environment group identified by env_id.
        :param types: Types of packages to install. Either an integer as a
            logical conjunction of CompsPackageType ids or a list of string
            package type ids (conditional, default, mandatory, optional).
        """
        assert dnf.util.is_string_type(env_id)
        solver = self._build_comps_solver()

        if not isinstance(types, int):
            types = libdnf.transaction.listToCompsPackageType(types)

        trans = solver._environment_install(env_id, types, exclude or set(), strict, exclude_groups)
        if not trans:
            return 0
        return self._add_comps_trans(trans)

    def environment_remove(self, env_id):
        # :api
        assert dnf.util.is_string_type(env_id)
        solver = self._build_comps_solver()
        trans = solver._environment_remove(env_id)
        return self._add_comps_trans(trans)

    def group_install(self, grp_id, pkg_types, exclude=None, strict=True):
        # :api
        """Installs packages of selected group
        :param pkg_types: Types of packages to install. Either an integer as a
            logical conjunction of CompsPackageType ids or a list of string
            package type ids (conditional, default, mandatory, optional).
        :param exclude: list of package name glob patterns
            that will be excluded from install set
        :param strict: boolean indicating whether group packages that
            exist but are non-installable due to e.g. dependency
            issues should be skipped (False) or cause transaction to
            fail to resolve (True)
        """
        def _pattern_to_pkgname(pattern):
            if dnf.util.is_glob_pattern(pattern):
                q = self.sack.query().filterm(name__glob=pattern)
                return map(lambda p: p.name, q)
            else:
                return (pattern,)

        assert dnf.util.is_string_type(grp_id)
        exclude_pkgnames = None
        if exclude:
            nested_excludes = [_pattern_to_pkgname(p) for p in exclude]
            exclude_pkgnames = itertools.chain.from_iterable(nested_excludes)

        solver = self._build_comps_solver()

        if not isinstance(pkg_types, int):
            pkg_types = libdnf.transaction.listToCompsPackageType(pkg_types)

        trans = solver._group_install(grp_id, pkg_types, exclude_pkgnames, strict)
        if not trans:
            return 0
        if strict:
            instlog = trans.install
        else:
            instlog = trans.install_opt
        logger.debug(_("Adding packages from group '%s': %s"),
                     grp_id, instlog)
        return self._add_comps_trans(trans)

    def env_group_install(self, patterns, types, strict=True, exclude=None, exclude_groups=None):
        q = CompsQuery(self.comps, self.history, CompsQuery.ENVIRONMENTS | CompsQuery.GROUPS,
                       CompsQuery.AVAILABLE)
        cnt = 0
        done = True
        for pattern in patterns:
            try:
                res = q.get(pattern)
            except dnf.exceptions.CompsError as err:
                logger.error(ucd(err))
                done = False
                continue
            for group_id in res.groups:
                if not exclude_groups or group_id not in exclude_groups:
                    cnt += self.group_install(group_id, types, exclude=exclude, strict=strict)
            for env_id in res.environments:
                cnt += self.environment_install(env_id, types, exclude=exclude, strict=strict,
                                                exclude_groups=exclude_groups)
        if not done and strict:
            raise dnf.exceptions.Error(_('Nothing to do.'))
        return cnt

    def group_remove(self, grp_id):
        # :api
        assert dnf.util.is_string_type(grp_id)
        solver = self._build_comps_solver()
        trans = solver._group_remove(grp_id)
        return self._add_comps_trans(trans)

    def env_group_remove(self, patterns):
        q = CompsQuery(self.comps, self.history,
                       CompsQuery.ENVIRONMENTS | CompsQuery.GROUPS,
                       CompsQuery.INSTALLED)
        try:
            res = q.get(*patterns)
        except dnf.exceptions.CompsError as err:
            logger.error("Warning: %s", ucd(err))
            raise dnf.exceptions.Error(_('No groups marked for removal.'))
        cnt = 0
        for env in res.environments:
            cnt += self.environment_remove(env)
        for grp in res.groups:
            cnt += self.group_remove(grp)
        return cnt

    def env_group_upgrade(self, patterns):
        q = CompsQuery(self.comps, self.history,
                       CompsQuery.GROUPS | CompsQuery.ENVIRONMENTS,
                       CompsQuery.INSTALLED)
        group_upgraded = False
        for pattern in patterns:
            try:
                res = q.get(pattern)
            except dnf.exceptions.CompsError as err:
                logger.error(ucd(err))
                continue
            for env in res.environments:
                try:
                    self.environment_upgrade(env)
                    group_upgraded = True
                except dnf.exceptions.CompsError as err:
                    logger.error(ucd(err))
                    continue
            for grp in res.groups:
                try:
                    self.group_upgrade(grp)
                    group_upgraded = True
                except dnf.exceptions.CompsError as err:
                    logger.error(ucd(err))
                    continue
        if not group_upgraded:
            msg = _('No group marked for upgrade.')
            raise dnf.cli.CliError(msg)

    def environment_upgrade(self, env_id):
        # :api
        assert dnf.util.is_string_type(env_id)
        solver = self._build_comps_solver()
        trans = solver._environment_upgrade(env_id)
        return self._add_comps_trans(trans)

    def group_upgrade(self, grp_id):
        # :api
        assert dnf.util.is_string_type(grp_id)
        solver = self._build_comps_solver()
        trans = solver._group_upgrade(grp_id)
        return self._add_comps_trans(trans)

    def _gpg_key_check(self):
        """Checks for the presence of GPG keys in the rpmdb.

        :return: 0 if there are no GPG keys in the rpmdb, and 1 if
           there are keys
        """
        gpgkeyschecked = self.conf.cachedir + '/.gpgkeyschecked.yum'
        if os.path.exists(gpgkeyschecked):
            return 1

        installroot = self.conf.installroot
        myts = dnf.rpm.transaction.initReadOnlyTransaction(root=installroot)
        myts.pushVSFlags(~(rpm._RPMVSF_NOSIGNATURES | rpm._RPMVSF_NODIGESTS))
        idx = myts.dbMatch('name', 'gpg-pubkey')
        keys = len(idx)
        del idx
        del myts

        if keys == 0:
            return 0
        else:
            mydir = os.path.dirname(gpgkeyschecked)
            if not os.path.exists(mydir):
                os.makedirs(mydir)

            fo = open(gpgkeyschecked, 'w')
            fo.close()
            del fo
            return 1

    def _install_multiarch(self, query, reponame=None, strict=True):
        already_inst, available = self._query_matches_installed(query)
        self._report_already_installed(already_inst)
        for packages in available:
            sltr = dnf.selector.Selector(self.sack)
            q = self.sack.query().filterm(pkg=packages)
            if self.conf.obsoletes:
                q = q.union(self.sack.query().filterm(obsoletes=q))
            sltr = sltr.set(pkg=q)
            if reponame is not None:
                sltr = sltr.set(reponame=reponame)
            self._goal.install(select=sltr, optional=(not strict))
        return len(available)

    def _categorize_specs(self, install, exclude):
        """
        Categorize :param install and :param exclude list into two groups each (packages and groups)

        :param install: list of specs, whether packages ('foo') or groups/modules ('@bar')
        :param exclude: list of specs, whether packages ('foo') or groups/modules ('@bar')
        :return: categorized install and exclude specs (stored in argparse.Namespace class)

        To access packages use: specs.pkg_specs,
        to access groups use: specs.grp_specs
        """
        install_specs = argparse.Namespace()
        exclude_specs = argparse.Namespace()
        _parse_specs(install_specs, install)
        _parse_specs(exclude_specs, exclude)

        return install_specs, exclude_specs

    def _exclude_package_specs(self, exclude_specs):
        glob_excludes = [exclude for exclude in exclude_specs.pkg_specs
                         if dnf.util.is_glob_pattern(exclude)]
        excludes = [exclude for exclude in exclude_specs.pkg_specs
                    if exclude not in glob_excludes]

        exclude_query = self.sack.query().filter(name=excludes)
        glob_exclude_query = self.sack.query().filter(name__glob=glob_excludes)

        self.sack.add_excludes(exclude_query)
        self.sack.add_excludes(glob_exclude_query)

    def _expand_groups(self, group_specs):
        groups = set()
        q = CompsQuery(self.comps, self.history,
                       CompsQuery.ENVIRONMENTS | CompsQuery.GROUPS,
                       CompsQuery.AVAILABLE | CompsQuery.INSTALLED)

        for pattern in group_specs:
            try:
                res = q.get(pattern)
            except dnf.exceptions.CompsError as err:
                logger.error("Warning: Module or %s", ucd(err))
                continue

            groups.update(res.groups)
            groups.update(res.environments)

            for environment_id in res.environments:
                environment = self.comps._environment_by_id(environment_id)
                for group in environment.groups_iter():
                    groups.add(group.id)

        return list(groups)

    def _install_groups(self, group_specs, excludes, skipped, strict=True):
        for group_spec in group_specs:
            try:
                types = self.conf.group_package_types

                if '/' in group_spec:
                    split = group_spec.split('/')
                    group_spec = split[0]
                    types = split[1].split(',')

                self.env_group_install([group_spec], types, strict, excludes.pkg_specs,
                                       excludes.grp_specs)
            except dnf.exceptions.Error:
                skipped.append("@" + group_spec)

    def install_specs(self, install, exclude=None, reponame=None, strict=True, forms=None):
        # :api
        if exclude is None:
            exclude = []
        no_match_group_specs = []
        error_group_specs = []
        no_match_pkg_specs = []
        error_pkg_specs = []
        install_specs, exclude_specs = self._categorize_specs(install, exclude)

        self._exclude_package_specs(exclude_specs)
        for spec in install_specs.pkg_specs:
            try:
                self.install(spec, reponame=reponame, strict=strict, forms=forms)
            except dnf.exceptions.MarkingError as e:
                logger.error(str(e))
                no_match_pkg_specs.append(spec)
        no_match_module_specs = []
        module_depsolv_errors = ()
        if WITH_MODULES and install_specs.grp_specs:
            try:
                module_base = dnf.module.module_base.ModuleBase(self)
                module_base.install(install_specs.grp_specs, strict)
            except dnf.exceptions.MarkingErrors as e:
                if e.no_match_group_specs:
                    for e_spec in e.no_match_group_specs:
                        no_match_module_specs.append(e_spec)
                if e.error_group_specs:
                    for e_spec in e.error_group_specs:
                        error_group_specs.append("@" + e_spec)
                module_depsolv_errors = e.module_depsolv_errors

        else:
            no_match_module_specs = install_specs.grp_specs

        if no_match_module_specs:
            exclude_specs.grp_specs = self._expand_groups(exclude_specs.grp_specs)
            self._install_groups(no_match_module_specs, exclude_specs, no_match_group_specs, strict)

        if no_match_group_specs or error_group_specs or no_match_pkg_specs or error_pkg_specs \
                or module_depsolv_errors:
            raise dnf.exceptions.MarkingErrors(no_match_group_specs=no_match_group_specs,
                                               error_group_specs=error_group_specs,
                                               no_match_pkg_specs=no_match_pkg_specs,
                                               error_pkg_specs=error_pkg_specs,
                                               module_depsolv_errors=module_depsolv_errors)

    def install(self, pkg_spec, reponame=None, strict=True, forms=None):
        # :api
        """Mark package(s) given by pkg_spec and reponame for installation."""

        subj = dnf.subject.Subject(pkg_spec)
        solution = subj.get_best_solution(self.sack, forms=forms, with_src=False)

        if self.conf.multilib_policy == "all" or subj._is_arch_specified(solution):
            q = solution['query']
            if reponame is not None:
                q.filterm(reponame=reponame)
            if not q:
                self._raise_package_not_found_error(pkg_spec, forms, reponame)
            return self._install_multiarch(q, reponame=reponame, strict=strict)

        elif self.conf.multilib_policy == "best":
            sltrs = subj._get_best_selectors(self,
                                             forms=forms,
                                             obsoletes=self.conf.obsoletes,
                                             reponame=reponame,
                                             reports=True,
                                             solution=solution)
            if not sltrs:
                self._raise_package_not_found_error(pkg_spec, forms, reponame)

            for sltr in sltrs:
                self._goal.install(select=sltr, optional=(not strict))
            return 1
        return 0

    def package_downgrade(self, pkg, strict=False):
        # :api
        if pkg._from_system:
            msg = 'downgrade_package() for an installed package.'
            raise NotImplementedError(msg)

        q = self.sack.query().installed().filterm(name=pkg.name, arch=[pkg.arch, "noarch"])
        if not q:
            msg = _("Package %s not installed, cannot downgrade it.")
            logger.warning(msg, pkg.name)
            raise dnf.exceptions.MarkingError(_('No match for argument: %s') % pkg.location, pkg.name)
        elif sorted(q)[0] > pkg:
            sltr = dnf.selector.Selector(self.sack)
            sltr.set(pkg=[pkg])
            self._goal.install(select=sltr, optional=(not strict))
            return 1
        else:
            msg = _("Package %s of lower version already installed, "
                    "cannot downgrade it.")
            logger.warning(msg, pkg.name)
            return 0

    def package_install(self, pkg, strict=True):
        # :api
        q = self.sack.query()._nevra(pkg.name, pkg.evr, pkg.arch)
        already_inst, available = self._query_matches_installed(q)
        if pkg in already_inst:
            self._report_already_installed([pkg])
        elif pkg not in itertools.chain.from_iterable(available):
            raise dnf.exceptions.PackageNotFoundError(_('No match for argument: %s') % pkg.location)
        else:
            sltr = dnf.selector.Selector(self.sack)
            sltr.set(pkg=[pkg])
            self._goal.install(select=sltr, optional=(not strict))
        return 1

    def package_reinstall(self, pkg):
        if self.sack.query().installed().filterm(name=pkg.name, evr=pkg.evr, arch=pkg.arch):
            self._goal.install(pkg)
            return 1
        msg = _("Package %s not installed, cannot reinstall it.")
        logger.warning(msg, str(pkg))
        raise dnf.exceptions.MarkingError(_('No match for argument: %s') % pkg.location, pkg.name)

    def package_remove(self, pkg):
        self._goal.erase(pkg)
        return 1

    def package_upgrade(self, pkg):
        # :api
        if pkg._from_system:
            msg = 'upgrade_package() for an installed package.'
            raise NotImplementedError(msg)

        if pkg.arch == 'src':
            msg = _("File %s is a source package and cannot be updated, ignoring.")
            logger.info(msg, pkg.location)
            return 0
        installed = self.sack.query().installed().apply()
        if self.conf.obsoletes and self.sack.query().filterm(pkg=[pkg]).filterm(obsoletes=installed):
            sltr = dnf.selector.Selector(self.sack)
            sltr.set(pkg=[pkg])
            self._goal.upgrade(select=sltr)
            return 1
        # do not filter by arch if the package is noarch
        if pkg.arch == "noarch":
            q = installed.filter(name=pkg.name)
        else:
            q = installed.filter(name=pkg.name, arch=[pkg.arch, "noarch"])
        if not q:
            msg = _("Package %s not installed, cannot update it.")
            logger.warning(msg, pkg.name)
            raise dnf.exceptions.MarkingError(
                _('No match for argument: %s') % pkg.location, pkg.name)
        elif sorted(q)[-1] < pkg:
            sltr = dnf.selector.Selector(self.sack)
            sltr.set(pkg=[pkg])
            self._goal.upgrade(select=sltr)
            return 1
        else:
            msg = _("The same or higher version of %s is already installed, "
                    "cannot update it.")
            logger.warning(msg, pkg.name)
            return 0

    def _upgrade_internal(self, query, obsoletes, reponame, pkg_spec=None):
        installed_all = self.sack.query().installed()
        # Add only relevant obsoletes to transaction => installed, upgrades
        q = query.intersection(self.sack.query().filterm(name=[pkg.name for pkg in installed_all]))
        installed_query = q.installed()
        if obsoletes:
            obsoletes = self.sack.query().available().filterm(
                obsoletes=installed_query.union(q.upgrades()))
            # add obsoletes into transaction
            query = query.union(obsoletes)
        if reponame is not None:
            query.filterm(reponame=reponame)
        query = self._merge_update_filters(query, pkg_spec=pkg_spec, upgrade=True)
        if query:
            # Given that we use libsolv's targeted transactions, we need to ensure that the transaction contains both
            # the new targeted version and also the current installed version (for the upgraded package). This is
            # because if it only contained the new version, libsolv would decide to reinstall the package even if it
            # had just a different buildtime or vendor but the same version
            # (https://github.com/openSUSE/libsolv/issues/287)
            #   - In general, the query already contains both the new and installed versions but not always.
            #     If repository-packages command is used, the installed packages are filtered out because they are from
            #     the @system repo. We need to add them back in.
            #   - However we need to add installed versions of just the packages that are being upgraded. We don't want
            #     to add all installed packages because it could increase the number of solutions for the transaction
            #     (especially without --best) and since libsolv prefers the smallest possible upgrade it could result
            #     in no upgrade even if there is one available. This is a problem in general but its critical with
            #     --security transactions (https://bugzilla.redhat.com/show_bug.cgi?id=2097757)
            #   - We want to add only the latest versions of installed packages, this is specifically for installonly
            #     packages. Otherwise if for example kernel-1 and kernel-3 were installed and present in the
            #     transaction libsolv could decide to install kernel-2 because it is an upgrade for kernel-1 even
            #     though we don't want it because there already is a newer version present.
            query = query.union(installed_all.latest().filter(name=[pkg.name for pkg in query]))
            sltr = dnf.selector.Selector(self.sack)
            sltr.set(pkg=query)
            self._goal.upgrade(select=sltr)
        return 1


    def upgrade(self, pkg_spec, reponame=None):
        # :api
        subj = dnf.subject.Subject(pkg_spec)
        solution = subj.get_best_solution(self.sack)
        q = solution["query"]
        if q:
            wildcard = dnf.util.is_glob_pattern(pkg_spec)
            # wildcard shouldn't print not installed packages
            # only solution with nevra.name provide packages with same name
            if not wildcard and solution['nevra'] and solution['nevra'].name:
                pkg_name = solution['nevra'].name
                installed = self.sack.query().installed().apply()
                obsoleters = q.filter(obsoletes=installed) \
                    if self.conf.obsoletes else self.sack.query().filterm(empty=True)
                if not obsoleters:
                    installed_name = installed.filter(name=pkg_name).apply()
                    if not installed_name:
                        msg = _('Package %s available, but not installed.')
                        logger.warning(msg, pkg_name)
                        raise dnf.exceptions.PackagesNotInstalledError(
                            _('No match for argument: %s') % pkg_spec, pkg_spec)
                    elif solution['nevra'].arch and not dnf.util.is_glob_pattern(solution['nevra'].arch):
                        if not installed_name.filterm(arch=solution['nevra'].arch):
                            msg = _('Package %s available, but installed for different architecture.')
                            logger.warning(msg, "{}.{}".format(pkg_name, solution['nevra'].arch))
            obsoletes = self.conf.obsoletes and solution['nevra'] \
                        and solution['nevra'].has_just_name()
            return self._upgrade_internal(q, obsoletes, reponame, pkg_spec)
        raise dnf.exceptions.MarkingError(_('No match for argument: %s') % pkg_spec, pkg_spec)

    def upgrade_all(self, reponame=None):
        # :api
        # provide only available packages to solver to trigger targeted upgrade
        # possibilities will be ignored
        # usage of selected packages will unify dnf behavior with other upgrade functions
        return self._upgrade_internal(
            self.sack.query(), self.conf.obsoletes, reponame, pkg_spec=None)

    def distro_sync(self, pkg_spec=None):
        if pkg_spec is None:
            self._goal.distupgrade_all()
        else:
            subject = dnf.subject.Subject(pkg_spec)
            solution = subject.get_best_solution(self.sack, with_src=False)
            solution["query"].filterm(reponame__neq=hawkey.SYSTEM_REPO_NAME)
            sltrs = subject._get_best_selectors(self, solution=solution,
                                                obsoletes=self.conf.obsoletes, reports=True)
            if not sltrs:
                logger.info(_('No package %s installed.'), pkg_spec)
                return 0
            for sltr in sltrs:
                self._goal.distupgrade(select=sltr)
        return 1

    def autoremove(self, forms=None, pkg_specs=None, grp_specs=None, filenames=None):
        # :api
        """Removes all 'leaf' packages from the system that were originally
        installed as dependencies of user-installed packages but which are
        no longer required by any such package."""

        if any([grp_specs, pkg_specs, filenames]):
            pkg_specs += filenames
            done = False
            # Remove groups.
            if grp_specs and forms:
                for grp_spec in grp_specs:
                    msg = _('Not a valid form: %s')
                    logger.warning(msg, grp_spec)
            elif grp_specs:
                if self.env_group_remove(grp_specs):
                    done = True

            for pkg_spec in pkg_specs:
                try:
                    self.remove(pkg_spec, forms=forms)
                except dnf.exceptions.MarkingError as e:
                    logger.info(str(e))
                else:
                    done = True

            if not done:
                logger.warning(_('No packages marked for removal.'))

        else:
            pkgs = self.sack.query()._unneeded(self.history.swdb,
                                               debug_solver=self.conf.debug_solver)
            for pkg in pkgs:
                self.package_remove(pkg)

    def remove(self, pkg_spec, reponame=None, forms=None):
        # :api
        """Mark the specified package for removal."""

        matches = dnf.subject.Subject(pkg_spec).get_best_query(self.sack, forms=forms)
        installed = [
            pkg for pkg in matches.installed()
            if reponame is None or
            self.history.repo(pkg) == reponame]
        if not installed:
            self._raise_package_not_installed_error(pkg_spec, forms, reponame)

        clean_deps = self.conf.clean_requirements_on_remove
        for pkg in installed:
            self._goal.erase(pkg, clean_deps=clean_deps)
        return len(installed)

    def reinstall(self, pkg_spec, old_reponame=None, new_reponame=None,
                  new_reponame_neq=None, remove_na=False):
        subj = dnf.subject.Subject(pkg_spec)
        q = subj.get_best_query(self.sack)
        installed_pkgs = [
            pkg for pkg in q.installed()
            if old_reponame is None or
            self.history.repo(pkg) == old_reponame]

        available_q = q.available()
        if new_reponame is not None:
            available_q.filterm(reponame=new_reponame)
        if new_reponame_neq is not None:
            available_q.filterm(reponame__neq=new_reponame_neq)
        available_nevra2pkg = dnf.query._per_nevra_dict(available_q)

        if not installed_pkgs:
            raise dnf.exceptions.PackagesNotInstalledError(
                'no package matched', pkg_spec, available_nevra2pkg.values())

        cnt = 0
        clean_deps = self.conf.clean_requirements_on_remove
        for installed_pkg in installed_pkgs:
            try:
                available_pkg = available_nevra2pkg[ucd(installed_pkg)]
            except KeyError:
                if not remove_na:
                    continue
                self._goal.erase(installed_pkg, clean_deps=clean_deps)
            else:
                self._goal.install(available_pkg)
            cnt += 1

        if cnt == 0:
            raise dnf.exceptions.PackagesNotAvailableError(
                'no package matched', pkg_spec, installed_pkgs)

        return cnt

    def downgrade(self, pkg_spec):
        # :api
        """Mark a package to be downgraded.

        This is equivalent to first removing the currently installed package,
        and then installing an older version.

        """
        return self.downgrade_to(pkg_spec)

    def downgrade_to(self, pkg_spec, strict=False):
        """Downgrade to specific version if specified otherwise downgrades
        to one version lower than the package installed.
        """
        subj = dnf.subject.Subject(pkg_spec)
        q = subj.get_best_query(self.sack)
        if not q:
            msg = _('No match for argument: %s') % pkg_spec
            raise dnf.exceptions.PackageNotFoundError(msg, pkg_spec)
        done = 0
        available_pkgs = q.available()
        available_pkg_names = list(available_pkgs._name_dict().keys())
        q_installed = self.sack.query().installed().filterm(name=available_pkg_names)
        if len(q_installed) == 0:
            msg = _('Packages for argument %s available, but not installed.') % pkg_spec
            raise dnf.exceptions.PackagesNotInstalledError(msg, pkg_spec, available_pkgs)
        for pkg_name in q_installed._name_dict().keys():
            downgrade_pkgs = available_pkgs.downgrades().filter(name=pkg_name)
            if not downgrade_pkgs:
                msg = _("Package %s of lowest version already installed, cannot downgrade it.")
                logger.warning(msg, pkg_name)
                continue
            sltr = dnf.selector.Selector(self.sack)
            sltr.set(pkg=downgrade_pkgs)
            self._goal.install(select=sltr, optional=(not strict))
            done = 1
        return done

    def provides(self, provides_spec):
        providers = self.sack.query().filterm(file__glob=provides_spec)
        if providers:
            return providers, [provides_spec]
        providers = dnf.query._by_provides(self.sack, provides_spec)
        if providers:
            return providers, [provides_spec]
        if provides_spec.startswith('/bin/') or provides_spec.startswith('/sbin/'):
            # compatibility for packages that didn't do UsrMove
            binary_provides = ['/usr' + provides_spec]
        elif provides_spec.startswith('/'):
            # provides_spec is a file path
            return providers, [provides_spec]
        else:
            # suppose that provides_spec is a command, search in /usr/sbin/
            binary_provides = [prefix + provides_spec
                               for prefix in ['/bin/', '/sbin/', '/usr/bin/', '/usr/sbin/']]
        return self.sack.query().filterm(file__glob=binary_provides), binary_provides

    def add_security_filters(self, cmp_type, types=(), advisory=(), bugzilla=(), cves=(), severity=()):
        #  :api
        """
        It modifies results of install, upgrade, and distrosync methods according to provided
        filters.

        :param cmp_type: only 'eq' or 'gte' allowed
        :param types: List or tuple with strings. E.g. 'bugfix', 'enhancement', 'newpackage',
        'security'
        :param advisory: List or tuple with strings. E.g.Eg. FEDORA-2201-123
        :param bugzilla: List or tuple with strings. Include packages that fix a Bugzilla ID,
        Eg. 123123.
        :param cves: List or tuple with strings. Include packages that fix a CVE
        (Common Vulnerabilities and Exposures) ID. Eg. CVE-2201-0123
        :param severity: List or tuple with strings. Includes packages that provide a fix
        for an issue of the specified severity.
        """
        cmp_dict = {'eq': '__eqg', 'gte': '__eqg__gt'}
        if cmp_type not in cmp_dict:
            raise ValueError("Unsupported value for `cmp_type`")
        cmp = cmp_dict[cmp_type]
        if types:
            key = 'advisory_type' + cmp
            self._update_security_options.setdefault(key, set()).update(types)
        if advisory:
            key = 'advisory' + cmp
            self._update_security_options.setdefault(key, set()).update(advisory)
        if bugzilla:
            key = 'advisory_bug' + cmp
            self._update_security_options.setdefault(key, set()).update(bugzilla)
        if cves:
            key = 'advisory_cve' + cmp
            self._update_security_options.setdefault(key, set()).update(cves)
        if severity:
            key = 'advisory_severity' + cmp
            self._update_security_options.setdefault(key, set()).update(severity)

    def reset_security_filters(self):
        #  :api
        """
        Reset all security filters
        """
        self._update_security_options = {}

    def _merge_update_filters(self, q, pkg_spec=None, warning=True, upgrade=False):
        """
        Merge Queries in _update_filters and return intersection with q Query
        @param q: Query
        @return: Query
        """
        if not (self._update_security_options or self._update_security_filters) or not q:
            return q
        merged_queries = self.sack.query().filterm(empty=True)
        if self._update_security_filters:
            for query in self._update_security_filters:
                merged_queries = merged_queries.union(query)

            self._update_security_filters = [merged_queries]
        if self._update_security_options:
            for filter_name, values in self._update_security_options.items():
                if upgrade:
                    filter_name = filter_name + '__upgrade'
                kwargs = {filter_name: values}
                merged_queries = merged_queries.union(q.filter(**kwargs))

        merged_queries = q.intersection(merged_queries)
        if not merged_queries:
            if warning:
                q = q.upgrades()
                count = len(q._name_dict().keys())
                if count > 0:
                    if pkg_spec is None:
                        msg1 = _("No security updates needed, but {} update "
                                 "available").format(count)
                        msg2 = _("No security updates needed, but {} updates "
                                 "available").format(count)
                        logger.warning(P_(msg1, msg2, count))
                    else:
                        msg1 = _('No security updates needed for "{}", but {} '
                                 'update available').format(pkg_spec, count)
                        msg2 = _('No security updates needed for "{}", but {} '
                                 'updates available').format(pkg_spec, count)
                        logger.warning(P_(msg1, msg2, count))
        return merged_queries

    def _get_key_for_package(self, po, askcb=None, fullaskcb=None):
        """Retrieve a key for a package. If needed, use the given
        callback to prompt whether the key should be imported.

        :param po: the package object to retrieve the key of
        :param askcb: Callback function to use to ask permission to
           import a key.  The arguments *askcb* should take are the
           package object, the userid of the key, and the keyid
        :param fullaskcb: Callback function to use to ask permission to
           import a key.  This differs from *askcb* in that it gets
           passed a dictionary so that we can expand the values passed.
        :raises: :class:`dnf.exceptions.Error` if there are errors
           retrieving the keys
        """
        if po._from_cmdline:
            # raise an exception, because po.repoid is not in self.repos
            msg = _('Unable to retrieve a key for a commandline package: %s')
            raise ValueError(msg % po)

        repo = self.repos[po.repoid]
        key_installed = repo.id in self._repo_set_imported_gpg_keys
        keyurls = [] if key_installed else repo.gpgkey

        def _prov_key_data(msg):
            msg += _('. Failing package is: %s') % (po) + '\n '
            msg += _('GPG Keys are configured as: %s') % \
                    (', '.join(repo.gpgkey))
            return msg

        user_cb_fail = False
        self._repo_set_imported_gpg_keys.add(repo.id)
        for keyurl in keyurls:
            keys = dnf.crypto.retrieve(keyurl, repo)

            for info in keys:
                # Check if key is already installed
                if misc.keyInstalled(self._ts, info.rpm_id, info.timestamp) >= 0:
                    msg = _('GPG key at %s (0x%s) is already installed')
                    logger.info(msg, keyurl, info.short_id)
                    continue

                # DNS Extension: create a key object, pass it to the verification class
                # and print its result as an advice to the user.
                if self.conf.gpgkey_dns_verification:
                    dns_input_key = dnf.dnssec.KeyInfo.from_rpm_key_object(info.userid,
                                                                           info.raw_key)
                    dns_result = dnf.dnssec.DNSSECKeyVerification.verify(dns_input_key)
                    logger.info(dnf.dnssec.nice_user_msg(dns_input_key, dns_result))

                # Try installing/updating GPG key
                info.url = keyurl
                if self.conf.gpgkey_dns_verification:
                    dnf.crypto.log_dns_key_import(info, dns_result)
                else:
                    dnf.crypto.log_key_import(info)
                rc = False
                if self.conf.assumeno:
                    rc = False
                elif self.conf.assumeyes:
                    # DNS Extension: We assume, that the key is trusted in case it is valid,
                    # its existence is explicitly denied or in case the domain is not signed
                    # and therefore there is no way to know for sure (this is mainly for
                    # backward compatibility)
                    # FAQ:
                    # * What is PROVEN_NONEXISTENCE?
                    #    In DNSSEC, your domain does not need to be signed, but this state
                    #    (not signed) has to be proven by the upper domain. e.g. when example.com.
                    #    is not signed, com. servers have to sign the message, that example.com.
                    #    does not have any signing key (KSK to be more precise).
                    if self.conf.gpgkey_dns_verification:
                        if dns_result in (dnf.dnssec.Validity.VALID,
                                          dnf.dnssec.Validity.PROVEN_NONEXISTENCE):
                            rc = True
                            logger.info(dnf.dnssec.any_msg(_("The key has been approved.")))
                        else:
                            rc = False
                            logger.info(dnf.dnssec.any_msg(_("The key has been rejected.")))
                    else:
                        rc = True

                # grab the .sig/.asc for the keyurl, if it exists if it
                # does check the signature on the key if it is signed by
                # one of our ca-keys for this repo or the global one then
                # rc = True else ask as normal.

                elif fullaskcb:
                    rc = fullaskcb({"po": po, "userid": info.userid,
                                    "hexkeyid": info.short_id,
                                    "keyurl": keyurl,
                                    "fingerprint": info.fingerprint,
                                    "timestamp": info.timestamp})
                elif askcb:
                    rc = askcb(po, info.userid, info.short_id)

                if not rc:
                    user_cb_fail = True
                    continue

                # Import the key
                # If rpm.RPMTRANS_FLAG_TEST in self._ts, gpg keys cannot be imported successfully
                # therefore the flag was removed for import operation
                test_flag = self._ts.isTsFlagSet(rpm.RPMTRANS_FLAG_TEST)
                if test_flag:
                    orig_flags = self._ts.getTsFlags()
                    self._ts.setFlags(orig_flags - rpm.RPMTRANS_FLAG_TEST)
                result = self._ts.pgpImportPubkey(misc.procgpgkey(info.raw_key))
                if test_flag:
                    self._ts.setFlags(orig_flags)
                if result != 0:
                    msg = _('Key import failed (code %d)') % result
                    raise dnf.exceptions.Error(_prov_key_data(msg))
                logger.info(_('Key imported successfully'))
                key_installed = True

        if not key_installed and user_cb_fail:
            raise dnf.exceptions.Error(_("Didn't install any keys"))

        if not key_installed:
            msg = _('The GPG keys listed for the "%s" repository are '
                    'already installed but they are not correct for this '
                    'package.\n'
                    'Check that the correct key URLs are configured for '
                    'this repository.') % repo.name
            raise dnf.exceptions.Error(_prov_key_data(msg))

        # Check if the newly installed keys helped
        result, errmsg = self._sig_check_pkg(po)
        if result != 0:
            if keyurls:
                msg = _("Import of key(s) didn't help, wrong key(s)?")
                logger.info(msg)
            errmsg = ucd(errmsg)
            raise dnf.exceptions.Error(_prov_key_data(errmsg))

    def package_import_key(self, pkg, askcb=None, fullaskcb=None):
        # :api
        """Retrieve a key for a package. If needed, use the given
        callback to prompt whether the key should be imported.

        :param pkg: the package object to retrieve the key of
        :param askcb: Callback function to use to ask permission to
           import a key.  The arguments *askcb* should take are the
           package object, the userid of the key, and the keyid
        :param fullaskcb: Callback function to use to ask permission to
           import a key.  This differs from *askcb* in that it gets
           passed a dictionary so that we can expand the values passed.
        :raises: :class:`dnf.exceptions.Error` if there are errors
           retrieving the keys
        """
        self._get_key_for_package(pkg, askcb, fullaskcb)

    def _run_rpm_check(self):
        results = []
        self._ts.check()
        for prob in self._ts.problems():
            #  Newer rpm (4.8.0+) has problem objects, older have just strings.
            #  Should probably move to using the new objects, when we can. For
            # now just be compatible.
            results.append(ucd(prob))

        return results

    def urlopen(self, url, repo=None, mode='w+b', **kwargs):
        # :api
        """
        Open the specified absolute url, return a file object
        which respects proxy setting even for non-repo downloads
        """
        return dnf.util._urlopen(url, self.conf, repo, mode, **kwargs)

    def _get_installonly_query(self, q=None):
        if q is None:
            q = self._sack.query(flags=hawkey.IGNORE_EXCLUDES)
        installonly = q.filter(provides=self.conf.installonlypkgs)
        return installonly

    def _report_icase_hint(self, pkg_spec):
        subj = dnf.subject.Subject(pkg_spec, ignore_case=True)
        solution = subj.get_best_solution(self.sack, with_nevra=True,
                                          with_provides=False, with_filenames=False)
        if solution['query'] and solution['nevra'] and solution['nevra'].name and \
                pkg_spec != solution['query'][0].name:
            logger.info(_("  * Maybe you meant: {}").format(solution['query'][0].name))

    def _select_remote_pkgs(self, install_pkgs):
        """ Check checksum of packages from local repositories and returns list packages from remote
        repositories that will be downloaded. Packages from commandline are skipped.

        :param install_pkgs: list of packages
        :return: list of remote pkgs
        """
        def _verification_of_packages(pkg_list, logger_msg):
            all_packages_verified = True
            for pkg in pkg_list:
                pkg_successfully_verified = False
                try:
                    pkg_successfully_verified = pkg.verifyLocalPkg()
                except Exception as e:
                    logger.critical(str(e))
                if pkg_successfully_verified is not True:
                    logger.critical(logger_msg.format(pkg, pkg.reponame))
                    all_packages_verified = False

            return all_packages_verified

        remote_pkgs = []
        local_repository_pkgs = []
        for pkg in install_pkgs:
            if pkg._is_local_pkg():
                if pkg.reponame != hawkey.CMDLINE_REPO_NAME:
                    local_repository_pkgs.append(pkg)
            else:
                remote_pkgs.append(pkg)

        msg = _('Package "{}" from local repository "{}" has incorrect checksum')
        if not _verification_of_packages(local_repository_pkgs, msg):
            raise dnf.exceptions.Error(
                _("Some packages from local repository have incorrect checksum"))

        if self.conf.cacheonly:
            msg = _('Package "{}" from repository "{}" has incorrect checksum')
            if not _verification_of_packages(remote_pkgs, msg):
                raise dnf.exceptions.Error(
                    _('Some packages have invalid cache, but cannot be downloaded due to '
                      '"--cacheonly" option'))
            remote_pkgs = []

        return remote_pkgs, local_repository_pkgs

    def _report_already_installed(self, packages):
        for pkg in packages:
            _msg_installed(pkg)

    def _raise_package_not_found_error(self, pkg_spec, forms, reponame):
        all_query = self.sack.query(flags=hawkey.IGNORE_EXCLUDES)
        subject = dnf.subject.Subject(pkg_spec)
        solution = subject.get_best_solution(
            self.sack, forms=forms, with_src=False, query=all_query)
        if reponame is not None:
            solution['query'].filterm(reponame=reponame)
        if not solution['query']:
            raise dnf.exceptions.PackageNotFoundError(_('No match for argument'), pkg_spec)
        else:
            with_regular_query = self.sack.query(flags=hawkey.IGNORE_REGULAR_EXCLUDES)
            with_regular_query = solution['query'].intersection(with_regular_query)
            # Modular filtering is applied on a package set that already has regular excludes
            # filtered out. So if a package wasn't filtered out by regular excludes, it must have
            # been filtered out by modularity.
            if with_regular_query:
                msg = _('All matches were filtered out by exclude filtering for argument')
            else:
                msg = _('All matches were filtered out by modular filtering for argument')
            raise dnf.exceptions.PackageNotFoundError(msg, pkg_spec)

    def _raise_package_not_installed_error(self, pkg_spec, forms, reponame):
        all_query = self.sack.query(flags=hawkey.IGNORE_EXCLUDES).installed()
        subject = dnf.subject.Subject(pkg_spec)
        solution = subject.get_best_solution(
            self.sack, forms=forms, with_src=False, query=all_query)

        if not solution['query']:
            raise dnf.exceptions.PackagesNotInstalledError(_('No match for argument'), pkg_spec)
        if reponame is not None:
            installed = [pkg for pkg in solution['query'] if self.history.repo(pkg) == reponame]
        else:
            installed = solution['query']
        if not installed:
            msg = _('All matches were installed from a different repository for argument')
        else:
            msg = _('All matches were filtered out by exclude filtering for argument')
        raise dnf.exceptions.PackagesNotInstalledError(msg, pkg_spec)

    def setup_loggers(self):
        # :api
        """
        Setup DNF file loggers based on given configuration file. The loggers are set the same
        way as if DNF was run from CLI.
        """
        self._logging._setup_from_dnf_conf(self.conf, file_loggers_only=True)

    def _skipped_packages(self, report_problems, transaction):
        """returns set of conflicting packages and set of packages with broken dependency that would
        be additionally installed when --best and --allowerasing"""
        if self._goal.actions & (hawkey.INSTALL | hawkey.UPGRADE | hawkey.UPGRADE_ALL):
            best = True
        else:
            best = False
        ng = deepcopy(self._goal)
        params = {"allow_uninstall": self._allow_erasing,
                  "force_best": best,
                  "ignore_weak": True}
        ret = ng.run(**params)
        if not ret and report_problems:
            msg = dnf.util._format_resolve_problems(ng.problem_rules())
            logger.warning(msg)
        problem_conflicts = set(ng.problem_conflicts(available=True))
        problem_dependency = set(ng.problem_broken_dependency(available=True)) - problem_conflicts

        def _nevra(item):
            return hawkey.NEVRA(name=item.name, epoch=item.epoch, version=item.version,
                                release=item.release, arch=item.arch)

        # Sometimes, pkg is not in transaction item, therefore, comparing by nevra
        transaction_nevras = [_nevra(tsi) for tsi in transaction]
        skipped_conflicts = set(
            [pkg for pkg in problem_conflicts if _nevra(pkg) not in transaction_nevras])
        skipped_dependency = set(
            [pkg for pkg in problem_dependency if _nevra(pkg) not in transaction_nevras])

        return skipped_conflicts, skipped_dependency


def _msg_installed(pkg):
    name = ucd(pkg)
    msg = _('Package %s is already installed.')
    logger.info(msg, name)
