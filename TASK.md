You're in a http tunneling with a golang services. Help me do these task.

[1]
Currently the tunnels service seems cannot support streaming like SSE. Take a look to the repo, and integrate supports for SSE Streaming and Binaries transferred streaming for like files downloading.

[2]
I want to have an update function so we can run `http-tunnels update` to able update the http-tunnels client application. This should fetch from the latest github release binary based on the detected OS and architecture.

[3]
I want to have a react based web interface. The purpose of this interface should shows any active connection, and every request-response logs in details.

For base components and styling, use ShadCN skills and all the components SHOULD and use your BEST EFFORT to use ShadCN.

Copy the css for shadcn in `.agents/reference-files/shadcn-theme.css`

This web interface will needs you to create a database to save all the data. If needed, migrate the current core implementation to use the database that are needed in this web interface.

If you need to use nodejs or javascript package, use `bun` runtime. Check `bun help` to see what it can do.

**Authentication**
Before using the web application, the user needs to authenticat. Create `/admin/auth/login` route to authenticate.
This application needs to be authenticated with an environment variable `WEB_PASSWORD`

After authenticated, it should be routed to `/admin` page

**Admin Layout**

Layout should be a sidebar and a main content.

The sidebar should have
- Branding on top
- List of Menus (scrollable)
- A hardcoded profile with Admin User Profile. In thse Admin User Profile have a logout door symbol when clicked to logout (redirect to `/admin/auth/logout`)

The Main content is based on the selected menu.

**Admin Page**

Currently we should only have 3 page:
1) List 1 - Dashboard
This should be all 

2) List 2 - Active Subdomain
A paginated table of active subdomain registered and active.
This table should have columns:
- The Subdomain (clickable to open details)
- How many Request Response Recorded
- Data transferred (in KB / MB / GB / TB)
- How long does this domain active (livetime)
- Action dropdown
  - Details
  - Delete (To remove the tunnels by admin side)
3) Active Subdomain Details
When details opened it should have be like details data, analytics and logging.

In Details data, show all tunnels data like:
- Tunnels Subdomain
- Created when
- How long does this been active
- Total Data Transferred

In Analytics Data you should show using ShadCN chart like in the reference `.agents/reference-files/chart-area-default.tsx`

There's should be a multiple chart like
- How many 2XX, 3XX, 4XX, and 5XX chart
- Data Inbound and Outbound from the server per tunne

After the chart you need to use

**Writer Notes on Data Recommendation**
Any analytics based data like request recorded should be saved in its own analytics table. This includes
- Request and Response Log (this probably include Data Trasnferred right?)
- Tunnel Creation Request Log

[4]
Update the `README.md` files for our application

[5]
Before finishing the task you need to documents your work patterns in this guidelines:
- `.agents/guidelines/authentication` -> Patterns to authenticate or deauth a user in the admin page
- `.agents/guidelines/data-fetching-mutation.md` -> Patterns do data fetching and mutation in the admin page
- `.agents/guidelines/routes.md` -> Patterns to do admin web based routing
- `.agents/guidelines/architecture.md` -> Application Arcitechture, the tunnel server and admin dashboard, the tunnel client
- `.agents/guidelines/README.md` -> The Index files for all the patterns and guideline documents. Take a look of the example in `.agents/reference-files/guidelines-readme-example.md`

After that, create `AGENTS.md` in the root file. This file should contain repository structure and also it needs to state, before doing any works, try to read the guidelines readme for this application patterns. Also add something like, if there's a drift in the patterns, update the patterns documentation. 

---

This is saved in `TASK.md` file.
You need to complete all the features without any exception to be stopped.

If you need clarification or needs confirmation of any request drift, use `ask_user` to ask or confirm.

Before you finish, use `ask_user` for the feedback result. You need to ask it until the user is staisfied or the user it's done. 