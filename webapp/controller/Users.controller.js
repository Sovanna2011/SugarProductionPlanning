sap.ui.define([
	"kss/spp/controller/BaseController",
	"sap/ui/model/json/JSONModel",
	"sap/ui/core/Fragment",
	"sap/m/MessageBox",
	"sap/m/MessageToast",
	"kss/spp/model/formatter"
], function (BaseController, JSONModel, Fragment, MessageBox, MessageToast, formatter) {
	"use strict";

	return BaseController.extend("kss.spp.controller.Users", {

		formatter: formatter,

		onInit: function () {
			this._model = this.getAppModel();
			this._model.setProperty("/users/includeInactive", false);

			// The dialog gets its own model: a half-typed account must not be
			// written into the list behind it.
			this._edit = new JSONModel(this._blankUser());
			this.getView().setModel(this._edit, "edit");

			this.getRouter().getRoute("users").attachPatternMatched(this._onRouteMatched, this);
		},

		_onRouteMatched: function () {
			// The server refuses this screen's data to anyone but an
			// administrator; sending them back is politer than a wall of 403s.
			var oSession = this.getSession();
			if (oSession.loginRequired && !oSession.permissions.manageUsers) {
				MessageToast.show(this.getText("notPermittedTitle"));
				this.getRouter().navTo("dashboard", {}, true);
				return;
			}
			this.onRefresh();
		},

		onNavBack: function () {
			this.getRouter().navTo("dashboard");
		},

		onRefresh: function () {
			var that = this;
			var bInactive = this._model.getProperty("/users/includeInactive");

			this._model.setProperty("/users/busy", true);
			return this.get("admin/users?includeInactive=" + (bInactive ? "true" : "false"))
				.then(function (aUsers) {
					that._model.setProperty("/users/list", aUsers || []);
				})
				.catch(function (err) { that.showError(err); })
				.finally(function () {
					that._model.setProperty("/users/busy", false);
				});
		},

		// --- create and edit --------------------------------------------------

		onNewUser: function () {
			this._edit.setData(this._blankUser());
			this._openDialog();
		},

		onEditUser: function (oEvent) {
			var oUser = oEvent.getSource().getBindingContext("app").getObject();

			// The user name is the audit subject and is never editable: an
			// account that changes its name would break the trail that points
			// at it. Everything else is fair game.
			this._edit.setData({
				id: oUser.id,
				username: oUser.username,
				displayName: oUser.displayName,
				email: oUser.email,
				status: oUser.status,
				remark: oUser.remark,
				version: oUser.version,
				password: "",
				isNew: false,
				roleAdmin: this._has(oUser, "ADMIN"),
				rolePlanner: this._has(oUser, "PLANNER"),
				roleWarehouse: this._has(oUser, "WAREHOUSE"),
				roleViewer: this._has(oUser, "VIEWER")
			});
			this._openDialog();
		},

		onSaveUser: function () {
			var that = this;
			var oData = this._edit.getData();

			var aRoles = [];
			if (oData.roleAdmin) { aRoles.push("ADMIN"); }
			if (oData.rolePlanner) { aRoles.push("PLANNER"); }
			if (oData.roleWarehouse) { aRoles.push("WAREHOUSE"); }
			if (oData.roleViewer) { aRoles.push("VIEWER"); }

			if (!aRoles.length) {
				MessageBox.warning(this.getText("pickARole"));
				return;
			}

			var oPayload = {
				id: oData.id || 0,
				username: oData.username,
				displayName: oData.displayName,
				email: oData.email,
				roles: aRoles,
				status: oData.status,
				remark: oData.remark,
				version: oData.version || 0
			};
			if (oData.isNew && oData.password) {
				oPayload.password = oData.password;
			}

			this._model.setProperty("/users/busy", true);
			this.post("admin/users", oPayload)
				.then(function (oResult) {
					that.onCancelUser();
					that.showWarnings(oResult.warnings);

					if (oResult.initialPassword) {
						// Shown once and never again: it is not stored in this
						// form anywhere.
						MessageBox.information(
							that.getText("initialPasswordIs", [oResult.user.username, oResult.initialPassword]),
							{ title: that.getText("userCreated") });
					} else {
						MessageToast.show(that.getText("userSaved", [oResult.user.username]));
					}
					return that.onRefresh();
				})
				.catch(function (err) { that.showError(err); })
				.finally(function () {
					that._model.setProperty("/users/busy", false);
				});
		},

		onCancelUser: function () {
			if (this._pDialog) {
				this._pDialog.then(function (oDialog) { oDialog.close(); });
			}
		},

		// --- passwords and sessions -------------------------------------------

		onResetPassword: function (oEvent) {
			var that = this;
			var oUser = oEvent.getSource().getBindingContext("app").getObject();

			MessageBox.confirm(this.getText("confirmResetPassword", [oUser.username]), {
				title: this.getText("resetPassword"),
				onClose: function (sAction) {
					if (sAction !== MessageBox.Action.OK) {
						return;
					}
					that._model.setProperty("/users/busy", true);
					that.post("admin/users/reset-password", { userId: oUser.id })
						.then(function (oResult) {
							MessageBox.information(
								that.getText("initialPasswordIs", [oUser.username, oResult.initialPassword]),
								{ title: that.getText("passwordReset") });
							return that.onRefresh();
						})
						.catch(function (err) { that.showError(err); })
						.finally(function () {
							that._model.setProperty("/users/busy", false);
						});
				}
			});
		},

		onSignOutUser: function (oEvent) {
			var that = this;
			var oUser = oEvent.getSource().getBindingContext("app").getObject();

			this._model.setProperty("/users/busy", true);
			this.post("admin/users/sign-out", { userId: oUser.id })
				.then(function (oResult) {
					MessageToast.show(that.getText("sessionsEnded", [oResult.sessionsEnded, oUser.username]));
				})
				.catch(function (err) { that.showError(err); })
				.finally(function () {
					that._model.setProperty("/users/busy", false);
				});
		},

		// --- helpers -----------------------------------------------------------

		_openDialog: function () {
			var that = this;
			var pDialog = this._pDialog || (this._pDialog = Fragment.load({
				id: this.getView().getId(),
				name: "kss.spp.view.UserDialog",
				controller: this
			}).then(function (oDialog) {
				that.getView().addDependent(oDialog);
				return oDialog;
			}));

			return pDialog.then(function (oDialog) { oDialog.open(); });
		},

		_has: function (oUser, sRole) {
			return (oUser.roles || []).indexOf(sRole) >= 0;
		},

		_blankUser: function () {
			return {
				id: 0,
				username: "",
				displayName: "",
				email: "",
				status: "ACTIVE",
				remark: "",
				version: 0,
				password: "",
				isNew: true,
				roleAdmin: false,
				rolePlanner: false,
				roleWarehouse: false,
				roleViewer: true
			};
		}
	});
});
