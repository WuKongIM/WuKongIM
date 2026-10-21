# Copyright 2005 Duke University
# Copyright (C) 2012-2016 Red Hat, Inc.
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
#
# Written by Seth Vidal

"""
Command line interface yum class and related.
"""

from __future__ import print_function
from __future__ import absolute_import
from __future__ import unicode_literals

try:
    from collections.abc import Sequence
except ImportError:
    from collections import Sequence
import datetime
import logging
import operator
import os
import random
import rpm
import sys
import time

import hawkey
import libdnf.transaction

from . import output
from dnf.cli import CliError
from dnf.i18n import ucd, _
import dnf
import dnf.cli.aliases
import dnf.cli.commands
import dnf.cli.commands.alias
import dnf.cli.commands.autoremove
import dnf.cli.commands.check
import dnf.cli.commands.clean
import dnf.cli.commands.deplist
import dnf.cli.commands.distrosync
import dnf.cli.commands.downgrade
import dnf.cli.commands.group
import dnf.cli.commands.history
import dnf.cli.commands.install
import dnf.cli.commands.makecache
import dnf.cli.commands.mark
import dnf.cli.commands.module
import dnf.cli.commands.reinstall
import dnf.cli.commands.remove
import dnf.cli.commands.repolist
import dnf.cli.commands.repoquery
import dnf.cli.commands.search
import dnf.cli.commands.shell
import dnf.cli.commands.swap
import dnf.cli.commands.updateinfo
import dnf.cli.commands.upgrade
import dnf.cli.commands.upgrademinimal
import dnf.cli.demand
import dnf.cli.format
import dnf.cli.option_parser
import dnf.conf
import dnf.conf.substitutions
import dnf.const
import dnf.db.history
import dnf.exceptions
import dnf.logging
import dnf.persistor
import dnf.plugin
import dnf.rpm
import dnf.sack
import dnf.transaction
import dnf.util
import dnf.yum.misc

logger = logging.getLogger('dnf')


def _add_pkg_simple_list_lens(data, pkg, indent=''):
    """ Get the length of each pkg's column. Add that to data.
        This "knows" about simpleList and printVer. """
    na = len(pkg.name) + 1 + len(pkg.arch) + len(indent)
    ver = len(pkg.evr)
    rid = len(pkg._from_repo)
    for (d, v) in (('na', na), ('ver', ver), ('rid', rid)):
        data[d].setdefault(v, 0)
        data[d][v] += 1


def _list_cmd_calc_columns(output, ypl):
    """ Work out the dynamic size of the columns to pass to fmtColumns. """
    data = {'na' : {}, 'ver' : {}, 'rid' : {}}
    for lst in (ypl.installed, ypl.available, ypl.extras, ypl.autoremove,
                ypl.updates, ypl.recent):
        for pkg in lst:
            _add_pkg_simple_list_lens(data, pkg)
    if len(ypl.obsoletes) > 0:
        for (npkg, opkg) in ypl.obsoletesTuples:
            _add_pkg_simple_list_lens(data, npkg)
            _add_pkg_simple_list_lens(data, opkg, indent=" " * 4)

    data = [data['na'], data['ver'], data['rid']]
    columns = output.calcColumns(data, remainder_column=1)
    return (-columns[0], -columns[1], -columns[2])


def print_versions(pkgs, base, output):
    def sm_ui_time(x):
        return time.strftime("%c", time.gmtime(x))

    rpmdb_sack = dnf.sack.rpmdb_sack(base)
    done = False
    for pkg in rpmdb_sack.query().installed().filterm(name=pkgs):
        if done:
            print("")
        done = True
        if pkg.epoch == '0':
            ver = '%s-%s.%s' % (pkg.version, pkg.release, pkg.arch)
        else:
            ver = '%s:%s-%s.%s' % (pkg.epoch,
                                   pkg.version, pkg.release, pkg.arch)
        name = output.term.bold(pkg.name)
        print(_("  Installed: %s-%s at %s") %(name, ver,
                                              sm_ui_time(pkg.installtime)))
        print(_("  Built    : %s at %s") % (pkg.packager if pkg.packager else "",
                                            sm_ui_time(pkg.buildtime)))
        # :hawkey, no changelist information yet
        # print(_("  Committed: %s at %s") % (pkg.committer,
        #                                    sm_ui_date(pkg.committime)))


def report_module_switch(switchedModules):
    msg1 = _("The operation would result in switching of module '{0}' stream '{1}' to "
             "stream '{2}'")
    for moduleName, streams in switchedModules.items():
        logger.warning(msg1.format(moduleName, streams[0], streams[1]))


class BaseCli(dnf.Base):
    """This is the base class for yum cli."""

    def __init__(self, conf=None):
        conf = conf or dnf.conf.Conf()
        super(BaseCli, self).__init__(conf=conf)
        self.output = output.Output(self, self.conf)

    def do_transaction(self, display=()):
        """Take care of package downloading, checking, user
        confirmation and actually running the transaction.

        :param display: `rpm.callback.TransactionProgress` object(s)
        :return: history database transaction ID or None
        """
        if dnf.base.WITH_MODULES:
            if not self.conf.module_stream_switch:
                switchedModules = dict(self._moduleContainer.getSwitchedStreams())
                if switchedModules:
                    report_module_switch(switchedModules)
                    msg = _("It is not possible to switch enabled streams of a module unless explicitly "
                            "enabled via configuration option module_stream_switch.\n"
                            "It is recommended to rather remove all installed content from the module, and "
                            "reset the module using '{prog} module reset <module_name>' command. After "
                            "you reset the module, you can install the other stream.").format(
                        prog=dnf.util.MAIN_PROG)
                    raise dnf.exceptions.Error(msg)

        trans = self.transaction
        pkg_str = self.output.list_transaction(trans)
        if pkg_str:
            logger.info(pkg_str)

        if trans:
            # Check which packages have to be downloaded
            install_pkgs = []
            rmpkgs = []
            install_only = True
            for tsi in trans:
                if tsi.action in dnf.transaction.FORWARD_ACTIONS:
                    install_pkgs.append(tsi.pkg)
                elif tsi.action in dnf.transaction.BACKWARD_ACTIONS:
                    install_only = False
                    rmpkgs.append(tsi.pkg)

            # Close the connection to the rpmdb so that rpm doesn't hold the
            # SIGINT handler during the downloads.
            del self._ts

            # report the total download size to the user
            if not install_pkgs:
                self.output.reportRemoveSize(rmpkgs)
            else:
                self.output.reportDownloadSize(install_pkgs, install_only)

        if trans or self._moduleContainer.isChanged() or \
                (self._history and (self._history.group or self._history.env)):
            # confirm with user
            if self.conf.downloadonly:
                logger.info(_("{prog} will only download packages for the transaction.").format(
                    prog=dnf.util.MAIN_PROG_UPPER))
            elif 'test' in self.conf.tsflags:
                logger.info(_("{prog} will only download packages, install gpg keys, and check the "
                              "transaction.").format(prog=dnf.util.MAIN_PROG_UPPER))
            if self._promptWanted():
                if self.conf.assumeno or not self.output.userconfirm():
                    raise CliError(_("Operation aborted."))
        else:
            logger.info(_('Nothing to do.'))
            return

        if trans:
            if install_pkgs:
                logger.info(_('Downloading Packages:'))
                try:
                    total_cb = self.output.download_callback_total_cb
                    self.download_packages(install_pkgs, self.output.progress, total_cb)
                except dnf.exceptions.DownloadError as e:
                    specific = dnf.cli.format.indent_block(ucd(e))
                    errstr = _('Error downloading packages:') + '\n%s' % specific
                    # setting the new line to prevent next chars being eaten up
                    # by carriage returns
                    print()
                    raise dnf.exceptions.Error(errstr)
            # Check GPG signatures
            self.gpgsigcheck(install_pkgs)

        if self.conf.downloadonly:
            return

        if not isinstance(display, Sequence):
            display = [display]
        display = [output.CliTransactionDisplay()] + list(display)
        tid = super(BaseCli, self).do_transaction(display)

        # display last transaction (which was closed during do_transaction())
        if tid is not None:
            trans = self.history.old([tid])[0]
            trans = dnf.db.group.RPMTransaction(self.history, trans._trans)
        else:
            trans = None

        if trans:
            # the post transaction summary is already written to log during
            # Base.do_transaction() so here only print the messages to the
            # user arranged in columns
            print()
            print('\n'.join(self.output.post_transaction_output(trans)))
            print()
            for tsi in trans:
                if tsi.state == libdnf.transaction.TransactionItemState_ERROR:
                    raise dnf.exceptions.Error(_('Transaction failed'))

        return tid

    def gpgsigcheck(self, pkgs):
        """Perform GPG signature verification on the given packages,
        installing keys if possible.

        :param pkgs: a list of package objects to verify the GPG
           signatures of
        :raises: Will raise :class:`Error` if there's a problem
        """
        error_messages = []
        for po in pkgs:
            result, errmsg = self._sig_check_pkg(po)

            if result == 0:
                # Verified ok, or verify not req'd
                continue

            elif result == 1:
                ay = self.conf.assumeyes and not self.conf.assumeno
                if (not sys.stdin or not sys.stdin.isatty()) and not ay:
                    raise dnf.exceptions.Error(_('Refusing to automatically import keys when running ' \
                            'unattended.\nUse "-y" to override.'))

                # the callback here expects to be able to take options which
                # userconfirm really doesn't... so fake it
                fn = lambda x, y, z: self.output.userconfirm()
                try:
                    self._get_key_for_package(po, fn)
                except (dnf.exceptions.Error, ValueError) as e:
                    error_messages.append(str(e))

            else:
                # Fatal error
                error_messages.append(errmsg)

        if error_messages:
            for msg in error_messages:
                logger.critical(msg)
            raise dnf.exceptions.Error(_("GPG check FAILED"))

    def latest_changelogs(self, package):
        """Return list of changelogs for package newer then installed version"""
        newest = None
        # find the date of the newest changelog for installed version of package
        # stored in rpmdb
        for mi in self._rpmconn.readonly_ts.dbMatch('name', package.name):
            changelogtimes = mi[rpm.RPMTAG_CHANGELOGTIME]
            if changelogtimes:
                newest = datetime.date.fromtimestamp(changelogtimes[0])
                break
        chlogs = [chlog for chlog in package.changelogs
                  if newest is None or chlog['timestamp'] > newest]
        return chlogs

    def format_changelog(self, changelog):
        """Return changelog formatted as in spec file"""
        chlog_str = '* %s %s\n%s\n' % (
            changelog['timestamp'].strftime("%a %b %d %X %Y"),
            dnf.i18n.ucd(changelog['author']),
            dnf.i18n.ucd(changelog['text']))
        return chlog_str

    def print_changelogs(self, packages):
        # group packages by src.rpm to avoid showing duplicate changelogs
        bysrpm = dict()
        for p in packages:
            # there are packages without source_name, use name then.
            bysrpm.setdefault(p.source_name or p.name, []).append(p)
        for source_name in sorted(bysrpm.keys()):
            bin_packages = bysrpm[source_name]
            print(_("Changelogs for {}").format(', '.join([str(pkg) for pkg in bin_packages])))
            for chl in self.latest_changelogs(bin_packages[0]):
                print(self.format_changelog(chl))

    def check_updates(self, patterns=(), reponame=None, print_=True, changelogs=False):
        """Check updates matching given *patterns* in selected repository."""
        ypl = self.returnPkgLists('upgrades', patterns, reponame=reponame)
        if self.conf.obsoletes or self.conf.verbose:
            typl = self.returnPkgLists('obsoletes', patterns, reponame=reponame)
            ypl.obsoletes = typl.obsoletes
            ypl.obsoletesTuples = typl.obsoletesTuples

        if print_:
            columns = _list_cmd_calc_columns(self.output, ypl)
            if len(ypl.updates) > 0:
                local_pkgs = {}
                highlight = self.output.term.MODE['bold']
                if highlight:
                    # Do the local/remote split we get in "yum updates"
                    for po in sorted(ypl.updates):
                        local = po.localPkg()
                        if os.path.exists(local) and po.verifyLocalPkg():
                            local_pkgs[(po.name, po.arch)] = po

                cul = self.conf.color_update_local
                cur = self.conf.color_update_remote
                self.output.listPkgs(ypl.updates, '', outputType='list',
                              highlight_na=local_pkgs, columns=columns,
                              highlight_modes={'=' : cul, 'not in' : cur})
                if changelogs:
                    self.print_changelogs(ypl.updates)

            if len(ypl.obsoletes) > 0:
                print(_('Obsoleting Packages'))
                # The tuple is (newPkg, oldPkg) ... so sort by new
                for obtup in sorted(ypl.obsoletesTuples,
                                    key=operator.itemgetter(0)):
                    self.output.updatesObsoletesList(obtup, 'obsoletes',
                                                     columns=columns)

        return ypl.updates or ypl.obsoletes

    def distro_sync_userlist(self, userlist):
        """ Upgrade or downgrade packages to match the latest versions available
            in the enabled repositories.

            :return: (exit_code, [ errors ])

            exit_code is::
                0 = we're done, exit
                1 = we've errored, exit with error string
                2 = we've got work yet to do, onto the next stage
        """
        oldcount = self._goal.req_length()
        if len(userlist) == 0:
            self.distro_sync()
        else:
            for pkg_spec in userlist:
                self.distro_sync(pkg_spec)

        cnt = self._goal.req_length() - oldcount
        if cnt <= 0 and not self._goal.req_has_distupgrade_all():
            msg = _('No packages marked for distribution synchronization.')
            raise dnf.exceptions.Error(msg)

    def downgradePkgs(self, specs=[], file_pkgs=[], strict=False):
        """Attempt to take the user specified list of packages or
        wildcards and downgrade them. If a complete version number is
        specified, attempt to downgrade them to the specified version

        :param specs: a list of names or wildcards specifying packages to downgrade
        :param file_pkgs: a list of pkg objects from local files
        """

        result = False
        for pkg in file_pkgs:
            try:
                self.package_downgrade(pkg, strict=strict)
                result = True
            except dnf.exceptions.MarkingError as e:
                logger.info(_('No match for argument: %s'),
                            self.output.term.bold(pkg.location))

        for arg in specs:
            try:
                self.downgrade_to(arg, strict=strict)
                result = True
            except dnf.exceptions.PackageNotFoundError as err:
                msg = _('No package %s available.')
                logger.info(msg, self.output.term.bold(arg))
            except dnf.exceptions.PackagesNotInstalledError as err:
                logger.info(_('Packages for argument %s available, but not installed.'),
                            self.output.term.bold(err.pkg_spec))
            except dnf.exceptions.MarkingError:
                assert False

        if not result:
            raise dnf.exceptions.Error(_('No packages marked for downgrade.'))

    def output_packages(self, basecmd, pkgnarrow='all', patterns=(), reponame=None):
        """Output selection *pkgnarrow* of packages matching *patterns* and *repoid*."""
        try:
            highlight = self.output.term.MODE['bold']
            ypl = self.returnPkgLists(
                pkgnarrow, patterns, installed_available=highlight, reponame=reponame)
        except dnf.exceptions.Error as e:
            return 1, [str(e)]
        else:
            update_pkgs = {}
            inst_pkgs = {}
            local_pkgs = {}

            columns = None
            if basecmd == 'list':
                # Dynamically size the columns
                columns = _list_cmd_calc_columns(self.output, ypl)

            if highlight and ypl.installed:
                #  If we have installed and available lists, then do the
                # highlighting for the installed packages so you can see what's
                # available to update, an extra, or newer than what we have.
                for pkg in (ypl.hidden_available +
                            ypl.reinstall_available +
                            ypl.old_available):
                    key = (pkg.name, pkg.arch)
                    if key not in update_pkgs or pkg > update_pkgs[key]:
                        update_pkgs[key] = pkg

            if highlight and ypl.available:
                #  If we have installed and available lists, then do the
                # highlighting for the available packages so you can see what's
                # available to install vs. update vs. old.
                for pkg in ypl.hidden_installed:
                    key = (pkg.name, pkg.arch)
                    if key not in inst_pkgs or pkg > inst_pkgs[key]:
                        inst_pkgs[key] = pkg

            if highlight and ypl.updates:
                # Do the local/remote split we get in "yum updates"
                for po in sorted(ypl.updates):
                    if po.reponame != hawkey.SYSTEM_REPO_NAME:
                        local_pkgs[(po.name, po.arch)] = po

            # Output the packages:
            clio = self.conf.color_list_installed_older
            clin = self.conf.color_list_installed_newer
            clir = self.conf.color_list_installed_reinstall
            clie = self.conf.color_list_installed_extra
            rip = self.output.listPkgs(ypl.installed, _('Installed Packages'), basecmd,
                                highlight_na=update_pkgs, columns=columns,
                                highlight_modes={'>' : clio, '<' : clin,
                                                 '=' : clir, 'not in' : clie})
            clau = self.conf.color_list_available_upgrade
            clad = self.conf.color_list_available_downgrade
            clar = self.conf.color_list_available_reinstall
            clai = self.conf.color_list_available_install
            rap = self.output.listPkgs(ypl.available, _('Available Packages'), basecmd,
                                highlight_na=inst_pkgs, columns=columns,
                                highlight_modes={'<' : clau, '>' : clad,
                                                 '=' : clar, 'not in' : clai})
            raep = self.output.listPkgs(ypl.autoremove, _('Autoremove Packages'),
                                basecmd, columns=columns)
            rep = self.output.listPkgs(ypl.extras, _('Extra Packages'), basecmd,
                                columns=columns)
            cul = self.conf.color_update_local
            cur = self.conf.color_update_remote
            rup = self.output.listPkgs(ypl.updates, _('Available Upgrades'), basecmd,
                                highlight_na=local_pkgs, columns=columns,
                                highlight_modes={'=' : cul, 'not in' : cur})

            # XXX put this into the ListCommand at some point
            if len(ypl.obsoletes) > 0 and basecmd == 'list':
            # if we've looked up obsolete lists and it's a list request
                rop = len(ypl.obsoletes)
                print(_('Obsoleting Packages'))
                for obtup in sorted(ypl.obsoletesTuples,
                                    key=operator.itemgetter(0)):
                    self.output.updatesObsoletesList(obtup, 'obsoletes',
                                                     columns=columns)
            else:
                rop = self.output.listPkgs(ypl.obsoletes, _('Obsoleting Packages'),
                                    basecmd, columns=columns)
            rrap = self.output.listPkgs(ypl.recent, _('Recently Added Packages'),
                                 basecmd, columns=columns)
            if len(patterns) and \
                    rrap == 0 and rop == 0 and rup == 0 and rep == 0 and rap == 0 and raep == 0 and rip == 0:
                raise dnf.exceptions.Error(_('No matching Packages to list'))

    def returnPkgLists(self, pkgnarrow='all', patterns=None,
                       installed_available=False, reponame=None):
        """Return a :class:`dnf.yum.misc.GenericHolder` object containing
        lists of package objects that match the given names or wildcards.

        :param pkgnarrow: a string specifying which types of packages
           lists to produce, such as updates, installed, available, etc.
        :param patterns: a list of names or wildcards specifying
           packages to list
        :param installed_available: whether the available package list
           is present as .hidden_available when doing all, available,
           or installed
        :param reponame: limit packages list to the given repository

        :return: a :class:`dnf.yum.misc.GenericHolder` instance with the
           following lists defined::

             available = list of packageObjects
             installed = list of packageObjects
             upgrades = tuples of packageObjects (updating, installed)
             extras = list of packageObjects
             obsoletes = tuples of packageObjects (obsoleting, installed)
             recent = list of packageObjects
        """

        done_hidden_available = False
        done_hidden_installed = False
        if installed_available and pkgnarrow == 'installed':
            done_hidden_available = True
            pkgnarrow = 'all'
        elif installed_available and pkgnarrow == 'available':
            done_hidden_installed = True
            pkgnarrow = 'all'

        ypl = self._do_package_lists(
            pkgnarrow, patterns, ignore_case=True, reponame=reponame)
        if self.conf.showdupesfromrepos:
            for pkg in ypl.reinstall_available:
                if not pkg.installed and not done_hidden_available:
                    ypl.available.append(pkg)

        if installed_available:
            ypl.hidden_available = ypl.available
            ypl.hidden_installed = ypl.installed
        if done_hidden_available:
            ypl.available = []
        if done_hidden_installed:
            ypl.installed = []
        return ypl

    def provides(self, args):
        """Print out a list of packages that provide the given file or
        feature.  This a cli wrapper to the provides methods in the
        rpmdb and pkgsack.

        :param args: the name of a file or feature to search for
        :return: (exit_code, [ errors ])

        exit_code is::

            0 = we're done, exit
            1 = we've errored, exit with error string
            2 = we've got work yet to do, onto the next stage
        """
        # always in showdups mode
        old_sdup = self.conf.showdupesfromrepos
        self.conf.showdupesfromrepos = True

        matches = []
        used_search_strings = []
        for spec in args:
            query, used_search_string = super(BaseCli, self).provides(spec)
            matches.extend(query)
            used_search_strings.extend(used_search_string)
        for pkg in sorted(matches):
            self.output.matchcallback_verbose(pkg, used_search_strings, args)
        self.conf.showdupesfromrepos = old_sdup

        if not matches:
            raise dnf.exceptions.Error(_('No matches found. If searching for a file, '
                                         'try specifying the full path or using a '
                                         'wildcard prefix ("*/") at the beginning.'))

    def _promptWanted(self):
        # shortcut for the always-off/always-on options
        if self.conf.assumeyes and not self.conf.assumeno:
            return False
        return True


class Cli(object):
    def __init__(self, base):
        self.base = base
        self.cli_commands = {}
        self.command = None
        self.demands = dnf.cli.demand.DemandSheet()  # :api

        self.register_command(dnf.cli.commands.alias.AliasCommand)
        self.register_command(dnf.cli.commands.autoremove.AutoremoveCommand)
        self.register_command(dnf.cli.commands.check.CheckCommand)
        self.register_command(dnf.cli.commands.clean.CleanCommand)
        self.register_command(dnf.cli.commands.distrosync.DistroSyncCommand)
        self.register_command(dnf.cli.commands.deplist.DeplistCommand)
        self.register_command(dnf.cli.commands.downgrade.DowngradeCommand)
        self.register_command(dnf.cli.commands.group.GroupCommand)
        self.register_command(dnf.cli.commands.history.HistoryCommand)
        self.register_command(dnf.cli.commands.install.InstallCommand)
        self.register_command(dnf.cli.commands.makecache.MakeCacheCommand)
        self.register_command(dnf.cli.commands.mark.MarkCommand)
        self.register_command(dnf.cli.commands.module.ModuleCommand)
        self.register_command(dnf.cli.commands.reinstall.ReinstallCommand)
        self.register_command(dnf.cli.commands.remove.RemoveCommand)
        self.register_command(dnf.cli.commands.repolist.RepoListCommand)
        self.register_command(dnf.cli.commands.repoquery.RepoQueryCommand)
        self.register_command(dnf.cli.commands.search.SearchCommand)
        self.register_command(dnf.cli.commands.shell.ShellCommand)
        self.register_command(dnf.cli.commands.swap.SwapCommand)
        self.register_command(dnf.cli.commands.updateinfo.UpdateInfoCommand)
        self.register_command(dnf.cli.commands.upgrade.UpgradeCommand)
        self.register_command(dnf.cli.commands.upgrademinimal.UpgradeMinimalCommand)
        self.register_command(dnf.cli.commands.InfoCommand)
        self.register_command(dnf.cli.commands.ListCommand)
        self.register_command(dnf.cli.commands.ProvidesCommand)
        self.register_command(dnf.cli.commands.CheckUpdateCommand)
        self.register_command(dnf.cli.commands.RepoPkgsCommand)
        self.register_command(dnf.cli.commands.HelpCommand)

    def _configure_repos(self, opts):
        self.base.read_all_repos(opts)
        if opts.repofrompath:
            for label, path in opts.repofrompath.items():
                this_repo = self.base.repos.add_new_repo(label, self.base.conf, baseurl=[path])
                this_repo._configure_from_options(opts)
                # do not let this repo to be disabled
                opts.repos_ed.append((label, "enable"))

        if opts.repo:
            opts.repos_ed.insert(0, ("*", "disable"))
            opts.repos_ed.extend([(r, "enable") for r in opts.repo])

        notmatch = set()

        # Process repo enables and disables in order
        try:
            for (repo, operation) in opts.repos_ed:
                repolist = self.base.repos.get_matching(repo)
                if not repolist:
                    if self.base.conf.strict and operation == "enable":
                        msg = _("Unknown repo: '%s'")
                        raise dnf.exceptions.RepoError(msg % repo)
                    notmatch.add(repo)

                if operation == "enable":
                    repolist.enable()
                else:
                    repolist.disable()
        except dnf.exceptions.ConfigError as e:
            logger.critical(e)
            self.optparser.print_help()
            sys.exit(1)

        for repo in notmatch:
            logger.warning(_("No repository match: %s"), repo)

        expired_repos = self.base._repo_persistor.get_expired_repos()
        if expired_repos is None:
            expired_repos = self.base.repos.keys()
        for rid in expired_repos:
            repo = self.base.repos.get(rid)
            if repo:
                repo._repo.expire()

        # setup the progress bars/callbacks
        (bar, self.base._ds_callback) = self.base.output.setup_progress_callbacks()
        self.base.repos.all().set_progress_bar(bar)
        key_import = output.CliKeyImport(self.base, self.base.output)
        self.base.repos.all()._set_key_import(key_import)

    def _log_essentials(self):
        logger.debug('{prog} version: %s'.format(prog=dnf.util.MAIN_PROG_UPPER),
                     dnf.const.VERSION)
        logger.log(dnf.logging.DDEBUG,
                        'Command: %s', self.cmdstring)
        logger.log(dnf.logging.DDEBUG,
                        'Installroot: %s', self.base.conf.installroot)
        logger.log(dnf.logging.DDEBUG, 'Releasever: %s',
                        self.base.conf.releasever)
        logger.debug("cachedir: %s", self.base.conf.cachedir)

    def _process_demands(self):
        demands = self.demands
        repos = self.base.repos

        if demands.root_user:
            if not dnf.util.am_i_root():
                raise dnf.exceptions.Error(
                    _('This command has to be run with superuser privileges '
                        '(under the root user on most systems).'))

        if demands.changelogs:
            for repo in repos.iter_enabled():
                repo.load_metadata_other = True

        if demands.cacheonly or self.base.conf.cacheonly:
            self.base.conf.cacheonly = True
            for repo in repos.values():
                repo._repo.setSyncStrategy(dnf.repo.SYNC_ONLY_CACHE)
        else:
            if demands.freshest_metadata:
                for repo in repos.iter_enabled():
                    repo._repo.expire()
            elif not demands.fresh_metadata:
                for repo in repos.values():
                    repo._repo.setSyncStrategy(dnf.repo.SYNC_LAZY)

        if demands.sack_activation:
            self.base.fill_sack(
                load_system_repo='auto' if self.demands.load_system_repo else False,
                load_available_repos=self.demands.available_repos)

    def _parse_commands(self, opts, args):
        """Check that the requested CLI command exists."""

        basecmd = opts.command
        command_cls = self.cli_commands.get(basecmd)
        if command_cls is None:
            logger.critical(_('No such command: %s. Please use %s --help'),
                            basecmd, sys.argv[0])
            if self.base.conf.plugins:
                logger.critical(_("It could be a {PROG} plugin command, "
                                  "try: \"{prog} install 'dnf-command(%s)'\"").format(
                    prog=dnf.util.MAIN_PROG, PROG=dnf.util.MAIN_PROG_UPPER), basecmd)
            else:
                logger.critical(_("It could be a {prog} plugin command, "
                                  "but loading of plugins is currently disabled.").format(
                    prog=dnf.util.MAIN_PROG_UPPER))
            raise CliError
        self.command = command_cls(self)

        logger.log(dnf.logging.DDEBUG, 'Base command: %s', basecmd)
        logger.log(dnf.logging.DDEBUG, 'Extra commands: %s', args)

    def configure(self, args, option_parser=None):
        """Parse command line arguments, and set up :attr:`self.base.conf` and
        :attr:`self.cmds`, as well as logger objects in base instance.

        :param args: a list of command line arguments
        :param option_parser: a class for parsing cli options
        """
        aliases = dnf.cli.aliases.Aliases()
        args = aliases.resolve(args)

        self.optparser = dnf.cli.option_parser.OptionParser() \
            if option_parser is None else option_parser
        opts = self.optparser.parse_main_args(args)

        # Just print out the version if that's what the user wanted
        if opts.version:
            print(dnf.const.VERSION)
            print_versions(self.base.conf.history_record_packages, self.base,
                           self.base.output)
            sys.exit(0)

        if opts.quiet:
            opts.debuglevel = 0
            opts.errorlevel = 2
        if opts.verbose:
            opts.debuglevel = opts.errorlevel = dnf.const.VERBOSE_LEVEL

        # Read up configuration options and initialize plugins
        try:
            if opts.cacheonly:
                self.base.conf._set_value("cachedir", self.base.conf.system_cachedir,
                                          dnf.conf.PRIO_DEFAULT)
                self.demands.cacheonly = True
            self.base.conf._configure_from_options(opts)
            self._read_conf_file(opts.releasever)
            if 'arch' in opts:
                self.base.conf.arch = opts.arch
            self.base.conf._adjust_conf_options()
        except (dnf.exceptions.ConfigError, ValueError) as e:
            logger.critical(_('Config error: %s'), e)
            sys.exit(1)
        except IOError as e:
            e = '%s: %s' % (ucd(str(e)), repr(e.filename))
            logger.critical(_('Config error: %s'), e)
            sys.exit(1)
        if opts.destdir is not None:
            self.base.conf.destdir = opts.destdir
            if not self.base.conf.downloadonly and opts.command not in (
                    'download', 'system-upgrade', 'reposync', 'modulesync'):
                logger.critical(_('--destdir or --downloaddir must be used with --downloadonly '
                                  'or download or system-upgrade command.')
                )
                sys.exit(1)
        if (opts.set_enabled or opts.set_disabled) and opts.command != 'config-manager':
            logger.critical(
                _('--enable, --set-enabled and --disable, --set-disabled '
                  'must be used with config-manager command.'))
            sys.exit(1)

        if opts.sleeptime is not None:
            time.sleep(random.randrange(opts.sleeptime * 60))

        # store the main commands & summaries, before plugins are loaded
        self.optparser.add_commands(self.cli_commands, 'main')
        # store the plugin commands & summaries
        self.base.init_plugins(opts.disableplugin, opts.enableplugin, self)
        self.optparser.add_commands(self.cli_commands,'plugin')

        # show help if no command specified
        # this is done here, because we first have the full
        # usage info after the plugins are loaded.
        if not opts.command:
            self.optparser.print_help()
            sys.exit(0)

        # save our original args out
        self.base.args = args
        # save out as a nice command string
        self.cmdstring = self.optparser.prog + ' '
        for arg in self.base.args:
            self.cmdstring += '%s ' % arg

        self._log_essentials()
        try:
            self._parse_commands(opts, args)
        except CliError:
            sys.exit(1)

        # show help for dnf <command> --help / --help-cmd
        if opts.help:
            self.optparser.print_help(self.command)
            sys.exit(0)

        opts = self.optparser.parse_command_args(self.command, args)

        if opts.allowerasing:
            self.demands.allow_erasing = opts.allowerasing
            self.base._allow_erasing = True
        if opts.freshest_metadata:
            self.demands.freshest_metadata = opts.freshest_metadata
        if opts.debugsolver:
            self.base.conf.debug_solver = True
        if opts.obsoletes:
            self.base.conf.obsoletes = True
        self.command.pre_configure()
        self.base.pre_configure_plugins()

        # with cachedir in place we can configure stuff depending on it:
        self.base._activate_persistor()

        self._configure_repos(opts)

        self.base.configure_plugins()

        self.base.conf._configure_from_options(opts)

        self.command.configure()

        if self.base.conf.destdir:
            dnf.util.ensure_dir(self.base.conf.destdir)
            self.base.repos.all().pkgdir = self.base.conf.destdir

        if self.base.conf.color != 'auto':
            self.base.output.term.reinit(color=self.base.conf.color)

        if rpm.expandMacro('%_pkgverify_level') in ('signature', 'all'):
            forcing = False
            for repo in self.base.repos.iter_enabled():
                if repo.gpgcheck:
                    continue
                repo.gpgcheck = True
                forcing = True
            if not self.base.conf.localpkg_gpgcheck:
                self.base.conf.localpkg_gpgcheck = True
                forcing = True
            if forcing:
                logger.warning(
                    _("Warning: Enforcing GPG signature check globally "
                      "as per active RPM security policy (see 'gpgcheck' in "
                      "dnf.conf(5) for how to squelch this message)"
                      )
                )

    def _read_conf_file(self, releasever=None):
        timer = dnf.logging.Timer('config')
        conf = self.base.conf

        # replace remote config path with downloaded file
        conf._check_remote_file('config_file_path')

        # search config file inside the installroot first
        conf._search_inside_installroot('config_file_path')

        # check whether a config file is requested from command line and the file exists
        filename = conf._get_value('config_file_path')
        if (conf._get_priority('config_file_path') == dnf.conf.PRIO_COMMANDLINE) and \
                not os.path.isfile(filename):
            raise dnf.exceptions.ConfigError(_('Config file "{}" does not exist').format(filename))

        # read config
        conf.read(priority=dnf.conf.PRIO_MAINCONFIG)

        # search reposdir file inside the installroot first
        from_root = conf._search_inside_installroot('reposdir')
        # Update vars from same root like repos were taken
        if conf._get_priority('varsdir') == dnf.conf.PRIO_COMMANDLINE:
            from_root = "/"
        subst = conf.substitutions
        subst.update_from_etc(from_root, varsdir=conf._get_value('varsdir'))
        # cachedir, logs, releasever, and gpgkey are taken from or stored in installroot
        if releasever is None and conf.releasever is None:
            releasever = dnf.rpm.detect_releasever(conf.installroot)
        elif releasever == '/':
            releasever = dnf.rpm.detect_releasever(releasever)
        if releasever is not None:
            conf.releasever = releasever
        if conf.releasever is None:
            logger.warning(_("Unable to detect release version (use '--releasever' to specify "
                             "release version)"))

        for opt in ('cachedir', 'logdir', 'persistdir'):
            conf.prepend_installroot(opt)

        self.base._logging._setup_from_dnf_conf(conf)

        timer()
        return conf

    def _populate_update_security_filter(self, opts, cmp_type='eq', all=None):
        """

        :param opts:
        :param cmp_type: string supported "eq", "gte"
        :param all:
        :return:
        """
        if (opts is None) and (all is None):
            return
        types = []

        if opts.bugfix or all:
            types.append('bugfix')
        if opts.enhancement or all:
            types.append('enhancement')
        if opts.newpackage or all:
            types.append('newpackage')
        if opts.security or all:
            types.append('security')

        self.base.add_security_filters(cmp_type, types=types, advisory=opts.advisory,
                                       bugzilla=opts.bugzilla, cves=opts.cves,
                                       severity=opts.severity)

    def redirect_logger(self, stdout=None, stderr=None):
        # :api
        """
        Change minimal logger level for terminal output to stdout and stderr according to specific
        command requirements
        @param stdout: logging.INFO, logging.WARNING, ...
        @param stderr:logging.INFO, logging.WARNING, ...
        """
        if stdout is not None:
            self.base._logging.stdout_handler.setLevel(stdout)
        if stderr is not None:
            self.base._logging.stderr_handler.setLevel(stderr)

    def redirect_repo_progress(self, fo=sys.stderr):
        progress = dnf.cli.progress.MultiFileProgressMeter(fo)
        self.base.output.progress = progress
        self.base.repos.all().set_progress_bar(progress)

    def _check_running_kernel(self):
        kernel = self.base.sack.get_running_kernel()
        if kernel is None:
            return

        q = self.base.sack.query().filterm(provides=kernel.name)
        q = q.installed()
        q.filterm(advisory_type='security')

        ikpkg = kernel
        for pkg in q:
            if pkg > ikpkg:
                ikpkg = pkg

        if ikpkg > kernel:
            print('Security: %s is an installed security update' % ikpkg)
            print('Security: %s is the currently running version' % kernel)

    def _option_conflict(self, option_string_1, option_string_2):
        print(self.optparser.print_usage())
        raise dnf.exceptions.Error(_("argument {}: not allowed with argument {}".format(
            option_string_1, option_string_2)))

    def register_command(self, command_cls):
        """Register a Command. :api"""
        for name in command_cls.aliases:
            if name in self.cli_commands:
                raise dnf.exceptions.ConfigError(_('Command "%s" already defined') % name)
            self.cli_commands[name] = command_cls

    def run(self):
        """Call the base command, and pass it the extended commands or
           arguments.

        :return: (exit_code, [ errors ])

        exit_code is::

            0 = we're done, exit
            1 = we've errored, exit with error string
            2 = we've got work yet to do, onto the next stage
        """
        self._process_demands()

        # Reports about excludes and includes (but not from plugins)
        if self.base.conf.excludepkgs:
            logger.debug(
                _('Excludes in dnf.conf: ') + ", ".join(sorted(set(self.base.conf.excludepkgs))))
        if self.base.conf.includepkgs:
            logger.debug(
                _('Includes in dnf.conf: ') + ", ".join(sorted(set(self.base.conf.includepkgs))))
        for repo in self.base.repos.iter_enabled():
            if repo.excludepkgs:
                logger.debug(_('Excludes in repo ') + repo.id + ": "
                             + ", ".join(sorted(set(repo.excludepkgs))))
            if repo.includepkgs:
                logger.debug(_('Includes in repo ') + repo.id + ": "
                             + ", ".join(sorted(set(repo.includepkgs))))

        return self.command.run()
